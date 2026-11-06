#!/bin/sh
# Render the two ZeoKeel wordmark candidates (2x reference for the 280px sidebar).
set -e
cd "$(dirname "$0")/.."
mkdir -p assets/wordmark
convert logo/logo.svg -resize 48x48 assets/wordmark/mark-48.png
# A: mark + ZeoKeel (bold, normal tracking)
convert -size 560x128 xc:'#F9FAFB' \
  assets/wordmark/mark-48.png -geometry +32+40 -composite \
  -font /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf -pointsize 34 -fill '#0F1115' \
  -annotate +96+74 'ZeoKeel' assets/wordmark/preview-a.png
# B: mark + ZeoKeel + boxed AGENT (mirrors DSH's "deepseek HARNESS" structure)
convert -size 720x128 xc:'#F9FAFB' \
  assets/wordmark/mark-48.png -geometry +32+40 -composite \
  -font /usr/share/fonts/opentype/urw-base35/NimbusSans-Bold.otf -pointsize 32 -fill '#0F1115' \
  -annotate +96+73 'ZeoKeel' assets/wordmark/base-b.png
convert -size 118x36 xc:'#F1F3F5' -fill none -stroke '#DADBDC' -strokewidth 1 \
  -draw 'roundrectangle 0.5,0.5 117.5,35.5 6,6' \
  -font /usr/share/fonts/opentype/urw-base35/NimbusSans-Bold.otf -pointsize 16 -kerning 2 \
  -stroke none -fill '#0F1115' -gravity center -annotate +1+0 'AGENT' assets/wordmark/agent-badge.png
W=$(convert -font /usr/share/fonts/opentype/urw-base35/NimbusSans-Bold.otf -pointsize 32 label:'ZeoKeel' -format '%w' info:)
X=$((96 + W + 16))
convert assets/wordmark/base-b.png assets/wordmark/agent-badge.png -gravity NorthWest -geometry +${X}+46 -composite assets/wordmark/preview-b.png
echo "wrote assets/wordmark/preview-a.png preview-b.png"
