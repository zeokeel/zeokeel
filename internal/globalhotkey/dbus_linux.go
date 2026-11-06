//go:build linux

package globalhotkey

// Minimal, dependency-free D-Bus client used by the Wayland GlobalShortcuts
// backend (wayland_linux.go). It implements only the slice of the D-Bus
// specification the xdg-desktop-portal request/response pattern needs:
//
//   - AF_UNIX transport: `unix:path=` addresses from $DBUS_SESSION_BUS_ADDRESS,
//     falling back to /run/user/$UID/bus (sessionBusAddress in wayland_linux.go);
//   - SASL EXTERNAL authentication with the caller's uid;
//   - the Hello handshake that assigns the connection its unique name;
//   - method calls with replies (method_return / error) matched by serial;
//   - unicast signals delivered on one channel (portal Request::Response and
//     GlobalShortcuts::Activated are emitted unicast to the requesting client,
//     so no AddMatch rule is required; a best-effort AddMatch is still sent for
//     portals that broadcast instead);
//   - little-endian encoding and both little/big-endian decoding;
//   - the type codes the portal API uses: y b n q i u x t d s o g v a( ) { }.
//
// Scope is deliberately small: no fd passing, no nested variants beyond the
// wire format, no introspection, no connection sharing. Values are marshalled
// from the handful of Go representations in this package (string, bool, integer
// widths, []any, []dbusShortcut, map[string]any) and unmarshalled into a small
// generic tree (dbusVariant/dbusDictEntry containers).
//
// The byte layout is pinned by exact-bytes tests in dbus_linux_test.go and, when
// available, by an interop test against a real dbus-daemon.

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Message types and flags (D-Bus specification, "Message Types").
const (
	dbusMethodCall   byte = 1
	dbusMethodReturn byte = 2
	dbusError        byte = 3
	dbusSignal       byte = 4

	dbusFlagNoReplyExpected byte = 0x1
	dbusFlagNoAutoStart     byte = 0x2
)

// Header field codes.
const (
	dbusFieldPath        byte = 1
	dbusFieldInterface   byte = 2
	dbusFieldMember      byte = 3
	dbusFieldErrorName   byte = 4
	dbusFieldReplySerial byte = 5
	dbusFieldDestination byte = 6
	dbusFieldSender      byte = 7
	dbusFieldSignature   byte = 8
	dbusFieldUnixFDs     byte = 9
)

const (
	// dbusMaxMessageSize is the protocol-wide limit (128 MiB).
	dbusMaxMessageSize = 128 << 20
	// dbusMaxArraySize is the protocol array limit (64 MiB).
	dbusMaxArraySize = 64 << 20
)

// dbusDialTimeout is the deadline for authentication and Hello; a variable so
// tests can shorten it.
var dbusDialTimeout = 2 * time.Second

// Well-known bus names/paths used by the client and the portal backend.
const (
	dbusBusName    = "org.freedesktop.DBus"
	dbusObjectPath = "/org/freedesktop/DBus"
)

// errDBusClosed reports use of a failed/closed connection.
var errDBusClosed = errors.New("globalhotkey: D-Bus connection is closed")

// dbusMessage is one decoded D-Bus message. Body is raw for the signature in
// Signature; bodyValues decodes it.
type dbusMessage struct {
	Type        byte
	Flags       byte
	Serial      uint32
	Path        string
	Interface   string
	Member      string
	ErrorName   string
	ReplySerial uint32
	Destination string
	Sender      string
	Signature   string
	Body        []byte

	// order is the message's wire byte order (little-endian when nil/marshal).
	order binary.ByteOrder
}

// bodyValues decodes the message body according to Signature.
func (m *dbusMessage) bodyValues() ([]any, error) {
	order := m.order
	if order == nil {
		order = binary.LittleEndian
	}
	return unmarshalDBusBody(m.Signature, m.Body, order)
}

// dbusVariant is a decoded VARIANT: its contained signature plus the value.
type dbusVariant struct {
	Signature string
	Value     any
}

// dbusDictEntry is a decoded DICT_ENTRY when it is not lifted into a Go map.
type dbusDictEntry struct {
	Key   any
	Value any
}

// dbusShortcut is one GlobalShortcuts shortcut tuple (id, options vardict).
type dbusShortcut struct {
	ID      string
	Options map[string]any
}

// --- signature parsing ------------------------------------------------------

// splitSignature splits a signature into its complete types.
func splitSignature(signature string) ([]string, error) {
	var types []string
	rest := signature
	for rest != "" {
		typ, after, err := firstCompleteType(rest)
		if err != nil {
			return nil, err
		}
		types = append(types, typ)
		rest = after
	}
	return types, nil
}

// firstCompleteType returns the first complete type of signature and the rest.
func firstCompleteType(signature string) (typ, rest string, err error) {
	if signature == "" {
		return "", "", errors.New("empty signature")
	}
	switch signature[0] {
	case 'y', 'b', 'n', 'q', 'i', 'u', 'x', 't', 'd', 's', 'o', 'g', 'v', 'h':
		return signature[:1], signature[1:], nil
	case 'a':
		elem, after, err := firstCompleteType(signature[1:])
		if err != nil {
			return "", "", err
		}
		return "a" + elem, after, nil
	case '(', '{':
		open := signature[0]
		closeCh := byte(')')
		if open == '{' {
			closeCh = '}'
		}
		depth := 0
		for i := 0; i < len(signature); i++ {
			switch signature[i] {
			case open:
				depth++
			case closeCh:
				depth--
				if depth == 0 {
					return signature[:i+1], signature[i+1:], nil
				}
			}
		}
		return "", "", fmt.Errorf("unterminated container in signature %q", signature)
	default:
		return "", "", fmt.Errorf("invalid type code %q in signature %q", signature[0], signature)
	}
}

// sigAlignment returns the wire alignment of a complete type.
func sigAlignment(typ string) (int, error) {
	if typ == "" {
		return 0, errors.New("empty type")
	}
	switch typ[0] {
	case 'y':
		return 1, nil
	case 'b', 'i', 'u', 'h':
		return 4, nil
	case 'n', 'q':
		return 2, nil
	case 'x', 't', 'd':
		return 8, nil
	case 's', 'o':
		return 4, nil
	case 'g', 'v':
		return 1, nil
	case 'a':
		return 4, nil
	case '(', '{':
		return 8, nil
	default:
		return 0, fmt.Errorf("invalid type code %q", typ[0])
	}
}

// containerFields splits the inside of a struct/dict-entry signature.
func containerFields(typ string) ([]string, error) {
	if len(typ) < 2 {
		return nil, fmt.Errorf("truncated container %q", typ)
	}
	return splitSignature(typ[1 : len(typ)-1])
}

// --- encoding ---------------------------------------------------------------

// dbusEncoder appends little-endian D-Bus values to a byte buffer.
type dbusEncoder struct {
	buf []byte
}

// align pads with nul bytes to the given alignment (relative to buffer start).
func (e *dbusEncoder) align(n int) {
	for len(e.buf)%n != 0 {
		e.buf = append(e.buf, 0)
	}
}

// putByte writes one byte.
func (e *dbusEncoder) putByte(b byte) { e.buf = append(e.buf, b) }

// putUint16 writes a little-endian uint16.
func (e *dbusEncoder) putUint16(v uint16) {
	e.align(2)
	e.buf = append(e.buf, byte(v), byte(v>>8))
}

// putUint32 writes a little-endian uint32.
func (e *dbusEncoder) putUint32(v uint32) {
	e.align(4)
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// putUint64 writes a little-endian uint64.
func (e *dbusEncoder) putUint64(v uint64) {
	e.align(8)
	e.buf = append(e.buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// putString writes a STRING/OBJECT_PATH body.
func (e *dbusEncoder) putString(s string) {
	e.align(4)
	e.putUint32(uint32(len(s)))
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0)
}

// putSignature writes a SIGNATURE body (1-byte length).
func (e *dbusEncoder) putSignature(sig string) error {
	if len(sig) > 255 {
		return fmt.Errorf("signature too long (%d bytes)", len(sig))
	}
	e.putByte(byte(len(sig)))
	e.buf = append(e.buf, sig...)
	e.buf = append(e.buf, 0)
	return nil
}

// writeValue encodes one value of the given complete type.
func (e *dbusEncoder) writeValue(typ string, value any) error {
	if typ == "" {
		return errors.New("empty type")
	}
	switch typ[0] {
	case 'y':
		v, ok := asUint64(value)
		if !ok || v > 0xff {
			return fmt.Errorf("type y: unsupported value %T", value)
		}
		e.putByte(byte(v))
	case 'b':
		v, ok := value.(bool)
		if !ok {
			return fmt.Errorf("type b: unsupported value %T", value)
		}
		if v {
			e.putUint32(1)
		} else {
			e.putUint32(0)
		}
	case 'n':
		v, ok := asUint64(value)
		if !ok || v > 0xffff {
			return fmt.Errorf("type n: unsupported value %T", value)
		}
		e.putUint16(uint16(v))
	case 'q':
		v, ok := asUint64(value)
		if !ok || v > 0xffff {
			return fmt.Errorf("type q: unsupported value %T", value)
		}
		e.putUint16(uint16(v))
	case 'i':
		v, ok := asUint64(value)
		if !ok || v > 0x7fffffff {
			return fmt.Errorf("type i: unsupported value %T", value)
		}
		e.putUint32(uint32(v))
	case 'u', 'h':
		v, ok := asUint64(value)
		if !ok || v > 0xffffffff {
			return fmt.Errorf("type %c: unsupported value %T", typ[0], value)
		}
		e.putUint32(uint32(v))
	case 'x', 't':
		v, ok := asUint64(value)
		if !ok {
			return fmt.Errorf("type %c: unsupported value %T", typ[0], value)
		}
		e.putUint64(v)
	case 'd':
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("type d: unsupported value %T", value)
		}
		e.putUint64(math.Float64bits(v))
	case 's', 'o':
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("type %c: unsupported value %T", typ[0], value)
		}
		e.align(4)
		e.putString(v)
	case 'g':
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("type g: unsupported value %T", value)
		}
		if err := e.putSignature(v); err != nil {
			return err
		}
	case 'v':
		variant, ok := value.(dbusVariant)
		if !ok {
			return fmt.Errorf("type v: unsupported value %T", value)
		}
		if err := e.putSignature(variant.Signature); err != nil {
			return err
		}
		return e.writeValue(variant.Signature, variant.Value)
	case 'a':
		return e.writeArray(typ, value)
	case '(':
		fields, err := containerFields(typ)
		if err != nil {
			return err
		}
		values, ok := value.([]any)
		if !ok || len(values) != len(fields) {
			return fmt.Errorf("type %s: unsupported value %T", typ, value)
		}
		e.align(8)
		for i, field := range fields {
			if err := e.writeValue(field, values[i]); err != nil {
				return err
			}
		}
	case '{':
		if err := e.writeDictEntry(typ, value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("cannot marshal unsupported type %q", typ)
	}
	return nil
}

// writeDictEntry encodes one dict entry (always 8-byte aligned).
func (e *dbusEncoder) writeDictEntry(typ string, value any) error {
	fields, err := containerFields(typ)
	if err != nil {
		return err
	}
	if len(fields) != 2 {
		return fmt.Errorf("dict entry %q needs exactly two types", typ)
	}
	entry, ok := value.(dbusDictEntry)
	if !ok {
		return fmt.Errorf("type %s: unsupported value %T", typ, value)
	}
	e.align(8)
	if err := e.writeValue(fields[0], entry.Key); err != nil {
		return err
	}
	return e.writeValue(fields[1], entry.Value)
}

// writeArray encodes an ARRAY: length field, element alignment padding, then
// elements. Per the specification the length excludes the padding after it and
// any padding after the last element.
func (e *dbusEncoder) writeArray(typ string, value any) error {
	elem, rest, err := firstCompleteType(typ[1:])
	if err != nil {
		return err
	}
	if rest != "" {
		return fmt.Errorf("type %s: malformed array type", typ)
	}
	elemAlign, err := sigAlignment(elem)
	if err != nil {
		return err
	}
	e.align(4)
	lengthPos := len(e.buf)
	e.buf = append(e.buf, 0, 0, 0, 0)
	e.align(elemAlign) // required even for an empty array
	dataStart := len(e.buf)

	switch v := value.(type) {
	case map[string]any:
		if elem[0] != '{' {
			return fmt.Errorf("type %s: cannot marshal a map", typ)
		}
		dictType, dictRest, err := firstCompleteType(elem)
		if err != nil {
			return err
		}
		if dictRest != "" {
			return fmt.Errorf("type %s: malformed dictionary type", typ)
		}
		dictFields, err := containerFields(dictType)
		if err != nil || len(dictFields) != 2 {
			return fmt.Errorf("type %s: malformed dictionary type", typ)
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			entryValue := v[key]
			if dictFields[1] == "v" {
				variant, verr := autoVariant(entryValue)
				if verr != nil {
					return fmt.Errorf("%s key %q: %w", typ, key, verr)
				}
				entryValue = variant
			}
			entry := dbusDictEntry{Key: key, Value: entryValue}
			if err := e.writeDictEntry(dictType, entry); err != nil {
				return err
			}
		}
	case []dbusShortcut:
		if elem != "(sa{sv})" {
			return fmt.Errorf("type %s: []dbusShortcut used with element type %q", typ, elem)
		}
		for _, shortcut := range v {
			if err := e.writeValue(elem, []any{shortcut.ID, shortcut.Options}); err != nil {
				return err
			}
		}
	case []string:
		for _, item := range v {
			if err := e.writeValue(elem, item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := e.writeValue(elem, item); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("type %s: unsupported array value %T", typ, value)
	}
	length := len(e.buf) - dataStart
	if length > dbusMaxArraySize {
		return fmt.Errorf("array of type %s exceeds the protocol limit", typ)
	}
	binary.LittleEndian.PutUint32(e.buf[lengthPos:], uint32(length))
	return nil
}

// dbusSignatureForValue infers the D-Bus signature of a Go value used inside a
// variant. It covers exactly the value shapes options dicts use.
func dbusSignatureForValue(value any) (string, error) {
	switch v := value.(type) {
	case dbusVariant:
		return v.Signature, nil
	case string:
		return "s", nil
	case bool:
		return "b", nil
	case uint8:
		return "y", nil
	case uint16:
		return "q", nil
	case int16:
		return "n", nil
	case uint32:
		return "u", nil
	case int32:
		return "i", nil
	case uint64:
		return "t", nil
	case int64:
		return "x", nil
	case float64:
		return "d", nil
	case []string:
		return "as", nil
	case map[string]any:
		return "a{sv}", nil
	default:
		return "", fmt.Errorf("cannot infer a D-Bus type for %T", value)
	}
}

// autoVariant wraps a plain Go value for a {sv} dictionary value.
func autoVariant(value any) (dbusVariant, error) {
	if variant, ok := value.(dbusVariant); ok {
		return variant, nil
	}
	sig, err := dbusSignatureForValue(value)
	if err != nil {
		return dbusVariant{}, err
	}
	return dbusVariant{Signature: sig, Value: value}, nil
}

// asUint64 coerces the fixed-width integer Go types used by this package.
func asUint64(value any) (uint64, bool) {
	switch v := value.(type) {
	case uint8:
		return uint64(v), true
	case uint16:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case uint64:
		return v, true
	case int16:
		return uint64(uint16(v)), true
	case int32:
		return uint64(uint32(v)), true
	case int64:
		return uint64(v), true
	case int:
		return uint64(v), true
	}
	return 0, false
}

// unmarshalDBusBody decodes a body of the given signature.
func unmarshalDBusBody(signature string, body []byte, order binary.ByteOrder) ([]any, error) {
	types, err := splitSignature(signature)
	if err != nil {
		return nil, err
	}
	if order == nil {
		order = binary.LittleEndian
	}
	decoder := &dbusDecoder{buf: body, order: order}
	values := make([]any, 0, len(types))
	for _, typ := range types {
		value, err := decoder.readValue(typ)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if decoder.pos != len(body) {
		return nil, fmt.Errorf("globalhotkey: %d trailing bytes in D-Bus body (signature %q)", len(body)-decoder.pos, signature)
	}
	return values, nil
}

// marshalDBusBody encodes body values (always little-endian).
func marshalDBusBody(signature string, values ...any) ([]byte, error) {
	types, err := splitSignature(signature)
	if err != nil {
		return nil, err
	}
	if len(types) != len(values) {
		return nil, fmt.Errorf("globalhotkey: signature %q has %d types but %d values were given", signature, len(types), len(values))
	}
	encoder := &dbusEncoder{}
	for i, typ := range types {
		if err := encoder.writeValue(typ, values[i]); err != nil {
			return nil, fmt.Errorf("globalhotkey: marshal %q argument %d: %w", signature, i, err)
		}
	}
	return encoder.buf, nil
}

// marshalDBusMessage encodes one complete message (little-endian).
func marshalDBusMessage(msg *dbusMessage) ([]byte, error) {
	if msg.Serial == 0 {
		return nil, errors.New("globalhotkey: D-Bus message serial must not be zero")
	}
	if msg.Signature == "" && len(msg.Body) != 0 {
		return nil, errors.New("globalhotkey: D-Bus body without a signature")
	}
	fields := &dbusEncoder{}
	addField := func(code byte, typ string, value any) error {
		fields.align(8)
		fields.putByte(code)
		return fields.writeValue("v", dbusVariant{Signature: typ, Value: value})
	}
	type fieldSpec struct {
		code  byte
		typ   string
		value any
	}
	specs := []fieldSpec{
		{dbusFieldPath, "o", msg.Path},
		{dbusFieldInterface, "s", msg.Interface},
		{dbusFieldMember, "s", msg.Member},
		{dbusFieldErrorName, "s", msg.ErrorName},
		{dbusFieldDestination, "s", msg.Destination},
		{dbusFieldSender, "s", msg.Sender},
		{dbusFieldSignature, "g", msg.Signature},
	}
	for _, spec := range specs {
		if spec.value == nil || spec.value == "" {
			continue
		}
		if err := addField(spec.code, spec.typ, spec.value); err != nil {
			return nil, err
		}
	}
	if msg.ReplySerial != 0 {
		if err := addField(dbusFieldReplySerial, "u", msg.ReplySerial); err != nil {
			return nil, err
		}
	}
	if msg.Flags&0x80 != 0 {
		return nil, errors.New("globalhotkey: invalid D-Bus flags")
	}

	headerFieldsLen := len(fields.buf)
	headerLen := 16 + headerFieldsLen
	padding := (8 - headerLen%8) % 8
	total := headerLen + padding + len(msg.Body)
	if total > dbusMaxMessageSize {
		return nil, errors.New("globalhotkey: D-Bus message exceeds the protocol limit")
	}
	out := make([]byte, 16, total)
	out[0] = 'l' // little-endian
	out[1] = msg.Type
	out[2] = msg.Flags
	out[3] = 1 // protocol major version
	binary.LittleEndian.PutUint32(out[4:], uint32(len(msg.Body)))
	binary.LittleEndian.PutUint32(out[8:], msg.Serial)
	binary.LittleEndian.PutUint32(out[12:], uint32(headerFieldsLen))
	out = append(out, fields.buf...)
	out = append(out, make([]byte, padding)...)
	out = append(out, msg.Body...)
	return out, nil
}

// readDBusMessage reads and decodes one message from the wire.
func readDBusMessage(r io.Reader) (*dbusMessage, error) {
	var fixed [16]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return nil, err
	}
	var order binary.ByteOrder = binary.LittleEndian
	switch fixed[0] {
	case 'l':
	case 'B':
		order = binary.BigEndian
	default:
		return nil, fmt.Errorf("globalhotkey: invalid D-Bus endianness flag %#x", fixed[0])
	}
	if fixed[3] != 1 {
		return nil, fmt.Errorf("globalhotkey: unsupported D-Bus protocol version %d", fixed[3])
	}
	bodyLen := order.Uint32(fixed[4:8])
	serial := order.Uint32(fixed[8:12])
	headerFieldsLen := order.Uint32(fixed[12:16])
	if bodyLen > dbusMaxMessageSize || headerFieldsLen > dbusMaxMessageSize {
		return nil, errors.New("globalhotkey: D-Bus message length exceeds the protocol limit")
	}
	if int(bodyLen)+int(headerFieldsLen)+16 > dbusMaxMessageSize {
		return nil, errors.New("globalhotkey: D-Bus message exceeds the protocol limit")
	}
	msg := &dbusMessage{
		Type:   fixed[1],
		Flags:  fixed[2],
		Serial: serial,
		order:  order,
	}
	headerBytes := make([]byte, headerFieldsLen)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return nil, err
	}
	if err := decodeDBusHeaderFields(msg, headerBytes); err != nil {
		return nil, err
	}
	headerLen := 16 + int(headerFieldsLen)
	padding := (8 - headerLen%8) % 8
	if padding > 0 {
		var pad [8]byte
		if _, err := io.ReadFull(r, pad[:padding]); err != nil {
			return nil, err
		}
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	msg.Body = body
	if msg.Signature == "" && len(body) != 0 {
		return nil, errors.New("globalhotkey: D-Bus body without a signature")
	}
	return msg, nil
}

// decodeDBusHeaderFields decodes the a(yv) header field array into msg.
func decodeDBusHeaderFields(msg *dbusMessage, headerBytes []byte) error {
	decoder := &dbusDecoder{buf: headerBytes, order: msg.order}
	for decoder.pos < len(headerBytes) {
		if err := decoder.align(8); err != nil {
			return err
		}
		code, err := decoder.readByte()
		if err != nil {
			return err
		}
		value, err := decoder.readValue("v")
		if err != nil {
			return err
		}
		variant, ok := value.(dbusVariant)
		if !ok {
			return errors.New("globalhotkey: malformed D-Bus header field")
		}
		switch code {
		case dbusFieldPath:
			if msg.Path, err = headerString(variant, "o"); err != nil {
				return err
			}
		case dbusFieldInterface:
			if msg.Interface, err = headerString(variant, "s"); err != nil {
				return err
			}
		case dbusFieldMember:
			if msg.Member, err = headerString(variant, "s"); err != nil {
				return err
			}
		case dbusFieldErrorName:
			if msg.ErrorName, err = headerString(variant, "s"); err != nil {
				return err
			}
		case dbusFieldReplySerial:
			serial, ok := variant.Value.(uint32)
			if variant.Signature != "u" || !ok {
				return fmt.Errorf("globalhotkey: invalid REPLY_SERIAL header field type %q", variant.Signature)
			}
			msg.ReplySerial = serial
		case dbusFieldDestination:
			if msg.Destination, err = headerString(variant, "s"); err != nil {
				return err
			}
		case dbusFieldSender:
			if msg.Sender, err = headerString(variant, "s"); err != nil {
				return err
			}
		case dbusFieldSignature:
			if msg.Signature, err = headerString(variant, "g"); err != nil {
				return err
			}
		case dbusFieldUnixFDs:
			if variant.Signature != "u" {
				return fmt.Errorf("globalhotkey: invalid UNIX_FDS header field type %q", variant.Signature)
			}
		default:
			// Unknown header fields must be ignored (forward compatibility).
		}
	}
	return nil
}

// headerString validates and extracts a string-like header field.
func headerString(variant dbusVariant, want string) (string, error) {
	if variant.Signature != want {
		return "", fmt.Errorf("globalhotkey: header field type %q, want %q", variant.Signature, want)
	}
	value, ok := variant.Value.(string)
	if !ok {
		return "", errors.New("globalhotkey: malformed string header field")
	}
	return value, nil
}

// --- decoding ---------------------------------------------------------------

// dbusDecoder reads values out of a byte block.
type dbusDecoder struct {
	buf   []byte
	pos   int
	order binary.ByteOrder
}

// align advances to the given alignment.
func (d *dbusDecoder) align(n int) error {
	padding := (n - d.pos%n) % n
	if d.pos+padding > len(d.buf) {
		return io.ErrUnexpectedEOF
	}
	d.pos += padding
	return nil
}

// readBytes returns the next n bytes.
func (d *dbusDecoder) readBytes(n int) ([]byte, error) {
	if n < 0 || d.pos+n > len(d.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	out := d.buf[d.pos : d.pos+n]
	d.pos += n
	return out, nil
}

// readByte reads one byte.
func (d *dbusDecoder) readByte() (byte, error) {
	bytes, err := d.readBytes(1)
	if err != nil {
		return 0, err
	}
	return bytes[0], nil
}

// readUint16 reads a uint16.
func (d *dbusDecoder) readUint16() (uint16, error) {
	if err := d.align(2); err != nil {
		return 0, err
	}
	bytes, err := d.readBytes(2)
	if err != nil {
		return 0, err
	}
	return d.order.Uint16(bytes), nil
}

// readUint32 reads a uint32.
func (d *dbusDecoder) readUint32() (uint32, error) {
	if err := d.align(4); err != nil {
		return 0, err
	}
	bytes, err := d.readBytes(4)
	if err != nil {
		return 0, err
	}
	return d.order.Uint32(bytes), nil
}

// readUint64 reads a uint64.
func (d *dbusDecoder) readUint64() (uint64, error) {
	if err := d.align(8); err != nil {
		return 0, err
	}
	bytes, err := d.readBytes(8)
	if err != nil {
		return 0, err
	}
	return d.order.Uint64(bytes), nil
}

// readString reads a STRING/OBJECT_PATH.
func (d *dbusDecoder) readString() (string, error) {
	length, err := d.readUint32()
	if err != nil {
		return "", err
	}
	if int(length) > dbusMaxMessageSize {
		return "", errors.New("globalhotkey: D-Bus string exceeds the protocol limit")
	}
	bytes, err := d.readBytes(int(length))
	if err != nil {
		return "", err
	}
	nul, err := d.readBytes(1)
	if err != nil {
		return "", err
	}
	if nul[0] != 0 {
		return "", errors.New("globalhotkey: D-Bus string is not nul-terminated")
	}
	return string(bytes), nil
}

// readSignature reads a SIGNATURE.
func (d *dbusDecoder) readSignature() (string, error) {
	length, err := d.readByte()
	if err != nil {
		return "", err
	}
	bytes, err := d.readBytes(int(length))
	if err != nil {
		return "", err
	}
	nul, err := d.readBytes(1)
	if err != nil {
		return "", err
	}
	if nul[0] != 0 {
		return "", errors.New("globalhotkey: D-Bus signature is not nul-terminated")
	}
	return string(bytes), nil
}

// readValue decodes one value of the given complete type.
func (d *dbusDecoder) readValue(typ string) (any, error) {
	if typ == "" {
		return nil, errors.New("empty type")
	}
	switch typ[0] {
	case 'y':
		return d.readByte()
	case 'b':
		value, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		if value > 1 {
			return nil, fmt.Errorf("globalhotkey: invalid D-Bus boolean %d", value)
		}
		return value == 1, nil
	case 'n':
		v, err := d.readUint16()
		return int16(v), err
	case 'q':
		return d.readUint16()
	case 'i':
		v, err := d.readUint32()
		return int32(v), err
	case 'u', 'h':
		return d.readUint32()
	case 'x':
		v, err := d.readUint64()
		return int64(v), err
	case 't':
		return d.readUint64()
	case 'd':
		v, err := d.readUint64()
		return math.Float64frombits(v), err
	case 's', 'o':
		return d.readString()
	case 'g':
		return d.readSignature()
	case 'v':
		signature, err := d.readSignature()
		if err != nil {
			return nil, err
		}
		value, err := d.readValue(signature)
		if err != nil {
			return nil, err
		}
		return dbusVariant{Signature: signature, Value: value}, nil
	case 'a':
		return d.readArray(typ)
	case '(':
		fields, err := containerFields(typ)
		if err != nil {
			return nil, err
		}
		if err := d.align(8); err != nil {
			return nil, err
		}
		values := make([]any, 0, len(fields))
		for _, field := range fields {
			value, err := d.readValue(field)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case '{':
		fields, err := containerFields(typ)
		if err != nil {
			return nil, err
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("globalhotkey: dict entry %q needs exactly two types", typ)
		}
		if err := d.align(8); err != nil {
			return nil, err
		}
		key, err := d.readValue(fields[0])
		if err != nil {
			return nil, err
		}
		value, err := d.readValue(fields[1])
		if err != nil {
			return nil, err
		}
		return dbusDictEntry{Key: key, Value: value}, nil
	default:
		return nil, fmt.Errorf("globalhotkey: cannot unmarshal unsupported type %q", typ)
	}
}

// readArray decodes an ARRAY. String-keyed dictionaries are lifted into
// map[string]any so callers can index portal result vardicts directly.
func (d *dbusDecoder) readArray(typ string) (any, error) {
	elem, rest, err := firstCompleteType(typ[1:])
	if err != nil {
		return nil, err
	}
	if rest != "" {
		return nil, fmt.Errorf("globalhotkey: malformed array type %q", typ)
	}
	elemAlign, err := sigAlignment(elem)
	if err != nil {
		return nil, err
	}
	length, err := d.readUint32()
	if err != nil {
		return nil, err
	}
	if length > dbusMaxArraySize {
		return nil, errors.New("globalhotkey: D-Bus array exceeds the protocol limit")
	}
	if err := d.align(elemAlign); err != nil { // required even when length is zero
		return nil, err
	}
	dataStart := d.pos
	if dataStart+int(length) > len(d.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	dataEnd := dataStart + int(length)

	// Lift string-keyed dict arrays into Go maps.
	if elem[0] == '{' {
		fields, err := containerFields(elem)
		if err != nil {
			return nil, err
		}
		if len(fields) == 2 && fields[0] == "s" {
			result := make(map[string]any)
			for d.pos < dataEnd {
				entryValue, err := d.readValue(elem)
				if err != nil {
					return nil, err
				}
				entry, ok := entryValue.(dbusDictEntry)
				if !ok {
					return nil, errors.New("globalhotkey: malformed D-Bus dictionary")
				}
				key, ok := entry.Key.(string)
				if !ok {
					return nil, errors.New("globalhotkey: non-string D-Bus dictionary key")
				}
				result[key] = entry.Value
			}
			if d.pos != dataEnd {
				return nil, errors.New("globalhotkey: D-Bus dictionary overruns its array length")
			}
			return result, nil
		}
	}

	values := make([]any, 0)
	for d.pos < dataEnd {
		value, err := d.readValue(elem)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if d.pos != dataEnd {
		return nil, errors.New("globalhotkey: D-Bus array overruns its declared length")
	}
	return values, nil
}

// variantString extracts a string from a decoded VARIANT.
func variantString(value any) (string, bool) {
	variant, ok := value.(dbusVariant)
	if !ok {
		text, textOK := value.(string)
		return text, textOK
	}
	text, ok := variant.Value.(string)
	return text, ok
}

// --- connection and client --------------------------------------------------

// dbusErrorReply is the error form of an ERROR message reply.
type dbusErrorReply struct {
	Name    string
	Message string
}

// Error renders the D-Bus error.
func (e *dbusErrorReply) Error() string {
	if e.Message != "" {
		return e.Name + ": " + e.Message
	}
	return e.Name
}

// dbusReply is one delivered method reply (or the connection failure).
type dbusReply struct {
	msg *dbusMessage
	err error
}

// dbusClient is one authenticated bus connection.
type dbusClient struct {
	conn   net.Conn
	reader *bufio.Reader
	unique string

	writeMu sync.Mutex
	serial  atomic.Uint32

	mu      sync.Mutex
	pending map[uint32]chan dbusReply
	closed  bool

	brokenFlag atomic.Bool
	signals    chan *dbusMessage
	done       chan struct{}
	closeOnce  sync.Once
}

// connectDBus dials, authenticates and says Hello. The context bounds the whole
// handshake.
func connectDBus(ctx context.Context, address string) (*dbusClient, error) {
	sockets, err := parseBusAddress(address)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, socket := range sockets {
		dialer := net.Dialer{Timeout: dbusDialTimeout}
		conn, err := dialer.DialContext(ctx, "unix", socket)
		if err != nil {
			lastErr = err
			continue
		}
		client, err := authenticateDBus(ctx, conn)
		if err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}
		return client, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no unix socket in the bus address")
	}
	return nil, fmt.Errorf("globalhotkey: connect session bus %q: %w", address, lastErr)
}

// authenticateDBus performs the SASL EXTERNAL handshake, sends BEGIN and says
// Hello on the newly authenticated connection.
func authenticateDBus(ctx context.Context, conn net.Conn) (*dbusClient, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dbusDialTimeout)
	}
	_ = conn.SetDeadline(deadline)
	defer conn.SetDeadline(time.Time{}) //nolint:errcheck // best effort

	reader := bufio.NewReader(conn)
	// Special credentials-passing nul byte (D-Bus specification, auth-protocol).
	if _, err := conn.Write([]byte{0}); err != nil {
		return nil, fmt.Errorf("globalhotkey: D-Bus auth: %w", err)
	}
	uidHex := hex.EncodeToString([]byte(strconv.Itoa(os.Getuid())))
	if _, err := conn.Write([]byte("AUTH EXTERNAL " + uidHex + "\r\n")); err != nil {
		return nil, fmt.Errorf("globalhotkey: D-Bus auth: %w", err)
	}
	authenticated := false
	for attempt := 0; attempt < 3 && !authenticated; attempt++ {
		line, err := readSASLResponse(reader)
		if err != nil {
			return nil, fmt.Errorf("globalhotkey: D-Bus auth: %w", err)
		}
		switch {
		case strings.HasPrefix(line, "OK "):
			authenticated = true
		case strings.HasPrefix(line, "DATA"):
			// EXTERNAL can request the identity in a DATA round-trip; an empty
			// response is the documented answer.
			if _, err := conn.Write([]byte("DATA\r\n")); err != nil {
				return nil, fmt.Errorf("globalhotkey: D-Bus auth: %w", err)
			}
		case strings.HasPrefix(line, "REJECTED"):
			return nil, fmt.Errorf("globalhotkey: D-Bus authentication rejected: %s", line)
		case strings.HasPrefix(line, "ERROR"):
			return nil, fmt.Errorf("globalhotkey: D-Bus authentication error: %s", line)
		default:
			return nil, fmt.Errorf("globalhotkey: unexpected D-Bus auth response %q", line)
		}
	}
	if !authenticated {
		return nil, errors.New("globalhotkey: D-Bus authentication did not complete")
	}
	if _, err := conn.Write([]byte("BEGIN\r\n")); err != nil {
		return nil, fmt.Errorf("globalhotkey: D-Bus auth: %w", err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("globalhotkey: D-Bus auth: %w", err)
	}

	client := &dbusClient{
		conn:    conn,
		reader:  reader,
		pending: make(map[uint32]chan dbusReply),
		signals: make(chan *dbusMessage, 32),
		done:    make(chan struct{}),
	}
	go client.readLoop()

	helloCtx, cancel := context.WithTimeout(ctx, dbusDialTimeout)
	defer cancel()
	reply, err := client.call(helloCtx, dbusBusName, dbusObjectPath, dbusBusName, "Hello", "")
	if err != nil {
		_ = client.close()
		return nil, fmt.Errorf("globalhotkey: D-Bus Hello: %w", err)
	}
	values, err := reply.bodyValues()
	if err != nil || len(values) != 1 {
		_ = client.close()
		return nil, fmt.Errorf("globalhotkey: malformed D-Bus Hello reply: %w", err)
	}
	unique, ok := values[0].(string)
	if !ok || unique == "" {
		_ = client.close()
		return nil, errors.New("globalhotkey: D-Bus Hello returned no unique name")
	}
	client.unique = unique
	return client, nil
}

// readSASLResponse reads one CRLF-terminated SASL response line.
func readSASLResponse(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if len(line) > 4096 {
		return "", errors.New("SASL response line too long")
	}
	return line, nil
}

// parseBusAddress extracts the unix socket paths from a D-Bus address. Abstract
// sockets are returned with the "@" prefix net.Dial expects on Linux.
func parseBusAddress(address string) ([]string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("globalhotkey: empty D-Bus bus address")
	}
	var sockets []string
	for _, part := range strings.Split(address, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "/") {
			sockets = append(sockets, part)
			continue
		}
		if !strings.HasPrefix(part, "unix:") {
			return nil, fmt.Errorf("globalhotkey: unsupported D-Bus transport in %q", part)
		}
		for _, param := range strings.Split(strings.TrimPrefix(part, "unix:"), ",") {
			key, value, ok := strings.Cut(param, "=")
			if !ok {
				continue
			}
			switch key {
			case "path":
				sockets = append(sockets, value)
			case "abstract":
				sockets = append(sockets, "@"+value)
			}
		}
	}
	if len(sockets) == 0 {
		return nil, fmt.Errorf("globalhotkey: no unix socket in D-Bus address %q", address)
	}
	return sockets, nil
}

// uniqueName returns the connection's unique bus name.
func (c *dbusClient) uniqueName() string { return c.unique }

// broken reports whether the connection failed or was closed.
func (c *dbusClient) broken() bool { return c.brokenFlag.Load() }

// call performs a method call and waits for its reply.
func (c *dbusClient) call(ctx context.Context, destination, path, iface, member, signature string, values ...any) (*dbusMessage, error) {
	msg, err := c.newMethodCall(false, destination, path, iface, member, signature, values...)
	if err != nil {
		return nil, err
	}
	replyCh := make(chan dbusReply, 1)
	c.mu.Lock()
	if c.closed || c.brokenFlag.Load() {
		c.mu.Unlock()
		return nil, errDBusClosed
	}
	c.pending[msg.Serial] = replyCh
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, msg.Serial)
		c.mu.Unlock()
	}()
	if err := c.writeMessageContext(ctx, msg); err != nil {
		return nil, err
	}
	select {
	case reply := <-replyCh:
		return reply.msg, reply.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		select {
		case reply := <-replyCh:
			return reply.msg, reply.err
		default:
		}
		return nil, errDBusClosed
	}
}

// notify sends a method call that does not expect a reply.
func (c *dbusClient) notify(ctx context.Context, destination, path, iface, member, signature string, values ...any) error {
	msg, err := c.newMethodCall(true, destination, path, iface, member, signature, values...)
	if err != nil {
		return err
	}
	return c.writeMessageContext(ctx, msg)
}

// newMethodCall builds one METHOD_CALL message.
func (c *dbusClient) newMethodCall(noReply bool, destination, path, iface, member, signature string, values ...any) (*dbusMessage, error) {
	body, err := marshalDBusBody(signature, values...)
	if err != nil {
		return nil, err
	}
	msg := &dbusMessage{
		Type:        dbusMethodCall,
		Serial:      c.serial.Add(1),
		Path:        path,
		Interface:   iface,
		Member:      member,
		Destination: destination,
		Signature:   signature,
		Body:        body,
	}
	if noReply {
		msg.Flags |= dbusFlagNoReplyExpected | dbusFlagNoAutoStart
	}
	return msg, nil
}

// writeMessageContext serializes one message, bounding the write by the
// context deadline when there is one (a wedged peer must not block teardown).
func (c *dbusClient) writeMessageContext(ctx context.Context, msg *dbusMessage) error {
	var deadline time.Time
	if ctx != nil {
		if d, ok := ctx.Deadline(); ok {
			deadline = d
		}
	}
	if c.brokenFlag.Load() {
		return errDBusClosed
	}
	wire, err := marshalDBusMessage(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.brokenFlag.Load() {
		return errDBusClosed
	}
	if !deadline.IsZero() {
		_ = c.conn.SetWriteDeadline(deadline)
		defer c.conn.SetWriteDeadline(time.Time{}) //nolint:errcheck // best effort
	}
	if _, err := c.conn.Write(wire); err != nil {
		c.brokenFlag.Store(true)
		return fmt.Errorf("globalhotkey: D-Bus write: %w", err)
	}
	return nil
}

// readLoop decodes incoming messages until the connection fails: replies are
// routed to callers by serial, signals go to the signals channel.
func (c *dbusClient) readLoop() {
	for {
		msg, err := readDBusMessage(c.reader)
		if err != nil {
			c.fail(err)
			return
		}
		switch msg.Type {
		case dbusMethodReturn, dbusError:
			c.deliverReply(msg)
		case dbusSignal:
			select {
			case c.signals <- msg:
			default:
				// Signals are low-volume; never stall the connection on a
				// slow consumer.
			}
		}
	}
}

// deliverReply hands a method reply to its waiting caller.
func (c *dbusClient) deliverReply(msg *dbusMessage) {
	c.mu.Lock()
	replyCh := c.pending[msg.ReplySerial]
	delete(c.pending, msg.ReplySerial)
	c.mu.Unlock()
	if replyCh == nil {
		return
	}
	if msg.Type == dbusError {
		text := ""
		if values, err := msg.bodyValues(); err == nil && len(values) > 0 {
			if s, ok := values[0].(string); ok {
				text = s
			}
		}
		replyCh <- dbusReply{err: &dbusErrorReply{Name: msg.ErrorName, Message: text}}
		return
	}
	replyCh <- dbusReply{msg: msg}
}

// fail marks the connection dead and unblocks waiting callers.
func (c *dbusClient) fail(err error) {
	c.brokenFlag.Store(true)
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[uint32]chan dbusReply)
	c.mu.Unlock()
	for _, replyCh := range pending {
		select {
		case replyCh <- dbusReply{err: err}:
		default:
		}
	}
	close(c.signals)
	close(c.done)
}

// close tears the connection down and waits for the reader to stop. Idempotent.
func (c *dbusClient) close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.brokenFlag.Store(true)
		closeErr = c.conn.Close()
		<-c.done
	})
	return closeErr
}
