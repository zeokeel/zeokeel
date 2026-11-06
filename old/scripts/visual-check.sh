#!/bin/sh
# ZeoKeel opt-in visual regression check.
#
# Renders the brand preview set headlessly (the same TestRender*NativePreview
# tests docs/design/preview documents), compares it with the committed goldens
# through ImageMagick, and prints one line per screen:
#
#   screen-name  diff-pixels  diff%  PASS/FAIL   (diff image path)
#
# It is NOT a CI gate: font stacks, font rasterizers and DPI differ across
# machines, so a cross-machine pixel gate would be flaky. It is the ZeoKeel-owned
# brand regression smoke check (docs/design/preview/README.md,
# docs/design/known-limitations.md section 4), not the retired DSH pixel parity
# gate (ADR 0001). Run it on a machine that can reproduce the goldens.
#
# Usage:
#   sh scripts/visual-check.sh              # compare; exit 1 over threshold
#   sh scripts/visual-check.sh --update     # refresh goldens (renders in place)
#   sh scripts/visual-check.sh --all        # also compare clock-bearing screens
#   sh scripts/visual-check.sh --threshold 3 --fuzz 1
#
# Options / environment:
#   --threshold PCT    max percent of differing pixels (default 2, or
#                      ZEOKEEL_VISUAL_THRESHOLD)
#   --fuzz PCT         ImageMagick per-pixel fuzz (default 0, or
#                      ZEOKEEL_VISUAL_FUZZ)
#   --out DIR          artifacts dir (default /tmp/zeokeel-visual, or
#                      ZEOKEEL_VISUAL_DIFF_DIR)
#   --all              include the known clock-bearing previews below
#
# Known non-deterministic previews (wall clock / absolute next-run timestamps).
# They are skipped by default; use --all to see their drift:
#   session-native-{light,dark}      message footer time (HH:MM)
#   automation-native-{light,dark}   next/last run timestamps
#   trajectory-native-{light,dark}   event times (HH:MM:SS)
#
# The script never leaves the work tree dirty: the committed goldens are backed
# up before the render and restored on exit. --update is the explicit exception:
# it keeps the freshly rendered previews so they can be reviewed and committed
# by hand.
set -eu
cd "$(dirname "$0")/.."

export PATH="/usr/local/go/bin:$PATH"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/gomod}"
export GOSUMDB=off
export CGO_ENABLED=0

GOLD=docs/design/preview
OUT=${ZEOKEEL_VISUAL_DIFF_DIR:-/tmp/zeokeel-visual}
THRESHOLD=${ZEOKEEL_VISUAL_THRESHOLD:-2}
FUZZ=${ZEOKEEL_VISUAL_FUZZ:-0}
UPDATE=0
ALL=0

die() { echo "visual-check: $*" >&2; exit 1; }

usage() {
	sed -n '2,40p' "$0"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--update) UPDATE=1 ;;
	--all) ALL=1 ;;
	--threshold)
		[ $# -ge 2 ] || die "--threshold needs a percent"
		THRESHOLD=$2
		shift
		;;
	--fuzz)
		[ $# -ge 2 ] || die "--fuzz needs a percent"
		FUZZ=$2
		shift
		;;
	--out)
		[ $# -ge 2 ] || die "--out needs a directory"
		OUT=$2
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "visual-check: unknown argument: $1" >&2
		usage >&2
		exit 2
		;;
	esac
	shift
done

command -v compare >/dev/null 2>&1 || die "ImageMagick 'compare' not found"
command -v identify >/dev/null 2>&1 || die "ImageMagick 'identify' not found"
command -v go >/dev/null 2>&1 || die "go not found (PATH=/usr/local/go/bin missing?)"
[ -d "$GOLD" ] || die "goldens dir missing: $GOLD"

# Known clock-bearing previews, by base name (light/dark suffixes removed).
DRIFT="session-native automation-native trajectory-native"
in_drift() {
	case " $DRIFT " in
	*" $1 "*) return 0 ;;
	esac
	return 1
}

rm -rf "$OUT"
mkdir -p "$OUT/before" "$OUT/diff"

echo "== visual-check: rendering previews (headless) =="
START=$(date +%s)
if [ "$UPDATE" = 1 ]; then
	ZEOKEEL_UPDATE_PREVIEWS=1 go test -count=1 ./internal/shell -run 'TestRender.*NativePreview' >/dev/null ||
		die "preview render failed"
	ELAPSED=$(($(date +%s) - START))
	echo "goldens refreshed in $GOLD in ${ELAPSED}s"
	echo "review the PNG changes and commit them by hand (this script never commits)"
	exit 0
fi

# Backup the committed goldens first, so the render below can overwrite the
# work tree and a trap can always put it back.
cp "$GOLD"/*.png "$OUT/before/"
restore() {
	for f in "$OUT/before"/*.png; do
		cp "$f" "$GOLD/$(basename "$f")"
	done
	echo "restored committed goldens under $GOLD"
}
trap restore EXIT HUP INT TERM

ZEOKEEL_UPDATE_PREVIEWS=1 go test -count=1 ./internal/shell -run 'TestRender.*NativePreview' >/dev/null ||
	die "preview render failed"

echo
printf '%-34s %10s %8s %-5s %s\n' "screen" "diff-px" "%" "state" "diff image"
failed=0
checked=0
skipped=0
for before in "$OUT/before"/*.png; do
	name=$(basename "$before")
	stem=${name%.png}
	base=${stem%-light}
	base=${base%-dark}
	if [ "$ALL" = 0 ] && in_drift "$base"; then
		skipped=$((skipped + 1))
		continue
	fi
	got="$GOLD/$name"
	dim_before=$(identify -format '%w %h' "$before")
	dim_after=$(identify -format '%w %h' "$got")
	if [ "$dim_before" != "$dim_after" ]; then
		printf '%-34s %10s %8s %-5s %s\n' "$stem" "-" "-" "FAIL" "size $dim_before -> $dim_after"
		failed=$((failed + 1))
		continue
	fi
	# compare exits 0 when identical and 1 when different; the AE metric is
	# printed either way. A real error exits 2 and yields no numeric metric.
	ae=$(compare -metric AE -fuzz "${FUZZ}%" "$before" "$GOLD/$name" "$OUT/diff/$name" 2>&1 || true)
	ae=$(printf '%s' "$ae" | tr -d '[:space:]')
	case "$ae" in
	'' | *[!0-9]*)
		printf '%-34s %10s %8s %-5s %s\n' "$stem" "-" "-" "FAIL" "compare error: $ae"
		failed=$((failed + 1))
		continue
		;;
	esac
	set -- $dim_before
	total=$(($1 * $2))
	pct=$(awk -v a="$ae" -v t="$total" 'BEGIN { printf "%.3f", (t > 0 ? a * 100 / t : 100) }')
	state=PASS
	if awk -v p="$pct" -v th="$THRESHOLD" 'BEGIN { exit !(p > th) }'; then
		state=FAIL
		failed=$((failed + 1))
	fi
	checked=$((checked + 1))
	printf '%-34s %10s %8s %-5s %s\n' "$stem" "$ae" "$pct" "$state" "$OUT/diff/$name"
done
ELAPSED=$(($(date +%s) - START))

echo
echo "threshold: ${THRESHOLD}%  fuzz: ${FUZZ}%  compared: $checked  skipped(known clock drift): $skipped  elapsed: ${ELAPSED}s"
echo "diff images: $OUT/diff/"
if [ "$failed" -gt 0 ]; then
	echo "VISUAL CHECK FAIL ($failed screen(s) over threshold)" >&2
	exit 1
fi
echo "VISUAL CHECK PASS"
