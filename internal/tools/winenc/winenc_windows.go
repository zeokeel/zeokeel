//go:build windows

package winenc

import (
	"strings"

	"golang.org/x/sys/windows"
)

// MB_ERR_INVALID_CHARS makes MultiByteToWideChar fail instead of silently
// replacing bytes, which is how StreamDecoder detects a split character.
const mbErrInvalidChars = 0x00000008

// procGetOEMCP is not exported by x/sys/windows, so resolve it lazily here.
var procGetOEMCP = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetOEMCP")

// codePageCandidates returns the process code pages to try, most likely first:
// the attached console's output code page when there is one, then the system
// ANSI code page (what .NET Framework's Encoding.Default uses when a GUI child
// has no console), then the OEM code page (cmd.exe built-ins).
func codePageCandidates() []uint32 {
	var pages []uint32
	add := func(cp uint32) {
		if cp == 0 {
			return
		}
		for _, seen := range pages {
			if seen == cp {
				return
			}
		}
		pages = append(pages, cp)
	}
	if cp, err := windows.GetConsoleOutputCP(); err == nil {
		add(cp)
	}
	add(windows.GetACP())
	if r, _, _ := procGetOEMCP.Call(); r != 0 {
		add(uint32(r))
	}
	return pages
}

// decodeComplete decodes data with the platform code page, requiring every byte
// to be valid so a split or invalid tail is reported instead of mangled.
func decodeComplete(data []byte) (string, bool) {
	if len(data) == 0 {
		return "", true
	}
	for _, cp := range codePageCandidates() {
		if text, ok := decodeCodePage(cp, data); ok {
			return text, true
		}
	}
	return "", false
}

func decodeCodePage(cp uint32, data []byte) (string, bool) {
	n, err := windows.MultiByteToWideChar(cp, mbErrInvalidChars, &data[0], int32(len(data)), nil, 0)
	if err != nil || n <= 0 {
		return "", false
	}
	buf := make([]uint16, n)
	written, err := windows.MultiByteToWideChar(cp, mbErrInvalidChars, &data[0], int32(len(data)), &buf[0], n)
	if err != nil || written <= 0 {
		return "", false
	}
	return windows.UTF16ToString(buf[:written]), true
}

// maxCharWidth is the longest multi-byte character these code pages can leave
// split across a chunk boundary (DBCS encodings use at most two bytes).
func maxCharWidth() int { return 2 }

// dbcsCodePages are the DBCS code pages whose characters can be split after a
// lead byte.
var dbcsCodePages = map[uint32]bool{932: true, 936: true, 949: true, 950: true, 1361: true}

// platformIncompleteTail reports a trailing lead byte of the active DBCS code
// page when the bytes before it decode cleanly, so StreamDecoder can hold it
// for the next chunk instead of losing it to a replacement rune.
func platformIncompleteTail(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	last := data[len(data)-1]
	if last < 0x81 || last > 0xfe {
		return 0
	}
	for _, cp := range codePageCandidates() {
		if !dbcsCodePages[cp] {
			continue
		}
		if len(data) == 1 {
			return 1
		}
		if _, ok := decodeComplete(data[:len(data)-1]); ok {
			return 1
		}
	}
	return 0
}

// invalidFallback replaces undecodable bytes so persisted JSON stays valid.
func invalidFallback(data []byte) string {
	return strings.ToValidUTF8(string(data), replacement)
}
