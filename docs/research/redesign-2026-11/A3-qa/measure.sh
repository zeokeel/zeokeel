#!/usr/bin/env bash
# A3-qa/measure.sh — ZeoKeel 视觉不变量检查（只依赖 ImageMagick 6/7）
#
# 把「接缝是否单线 / accent 块是否超预算 / 圆角是否在档位内 / 关键几何是否漂移 /
# 暗色 ramp 是否反向」变成可复跑的确定性检查，并给出与基线目录的差异清单。
#
# 用法：
#   A3-qa/measure.sh [快照目录] [--baseline DIR] [--strict] [--list]
#   A3-qa/measure.sh .cache/ui-conformance-v2
#   A3-qa/measure.sh .cache/impl-shell --baseline .cache/ui-conformance --strict
#
# 输出：
#   终端报告；.cache/ui-conformance-qa/<slug>.report.txt
#                 .cache/ui-conformance-qa/<slug>.tsv
#                 （TSV 列：check  screen  metric  value  expected  verdict）
#   --strict 有 FAIL 时退出码 1。
#
# 期望值来源（2026-11 token；改 token 改下面 CONFIG 段）：
#   边框 L1 6% / L2 9% / L3 15% ink（暗色 7% / 10% / 16% white）
#   圆角 4 / 6 / 8 / 12 / 16（RadiusMax 只给头像，不参与卡片判定）
#   accent 填充块 ≤2，口径 = ≥24×24 连通块（图标/下划线/描边不计）
#   阅读列 TranscriptWidth 960；正文 ProseMeasure 640（+20 容差）
#
set -uo pipefail

# ── CONFIG ────────────────────────────────────────────────────────────────
INK="0F1115"
BORDER_L1_PCT=6
BORDER_L2_PCT=9
BORDER_L3_PCT=15
DARK_L2_PCT=10
RADII="4 6 8 12 16"
ACCENT_LIGHT="#0D44F6"
ACCENT_DARK="#3CBBFA"
ACCENT_BLOCK_MIN=24
COLUMN_MAX=968        # TranscriptWidth 960 + 8
PROSE_MAX=660         # ProseMeasure 640 + 20
SEAM_COLOR_TOL=8
BAND_H=84   # 2026-11: 顶带 48 + tabs 36
# ──────────────────────────────────────────────────────────────────────────

command -v convert  >/dev/null 2>&1 || { echo "需要 ImageMagick convert"; exit 2; }
command -v identify >/dev/null 2>&1 || { echo "需要 ImageMagick identify"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)
DIR=""; BASELINE="$REPO/.cache/ui-conformance"; STRICT=0; LIST=0
while [ $# -gt 0 ]; do
  case "$1" in
    --baseline)   BASELINE="${2:-}"; shift 2 ;;
    --baseline=*) BASELINE="${1#*=}"; shift ;;
    --strict) STRICT=1; shift ;;
    --list)   LIST=1; shift ;;
    -h|--help) sed -n '2,32p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) DIR="$1"; shift ;;
  esac
done
if [ -z "$DIR" ]; then
  for c in "$REPO/.cache/ui-conformance-v2" "$REPO/.cache/ui-conformance"; do
    [ -f "$c/p1-chat.png" ] && { DIR="$c"; break; }
  done
fi
[ -n "$DIR" ] && [ -d "$DIR" ] || { echo "找不到快照目录：${DIR:-<empty>}（--help）"; exit 2; }
DIR=$(cd "$DIR" && pwd)
if [ -n "$BASELINE" ] && [ -d "$BASELINE" ]; then BASELINE=$(cd "$BASELINE" && pwd); else BASELINE=""; fi

OUT="$REPO/.cache/ui-conformance-qa"; mkdir -p "$OUT" "$OUT/tmp"
SLUG=$(printf '%s' "${DIR#$REPO/.cache/}" | tr '/' '-'); [ -n "$SLUG" ] || SLUG=snapshot
REPORT="$OUT/$SLUG.report.txt"; TSV="$OUT/$SLUG.tsv"
: >"$OUT/tmp/rows.tsv"; : >"$REPORT"

PASS=0; FAIL=0; WARN=0; SKIP=0
say() { printf '%s\n' "$*" | tee -a "$REPORT"; }
row() { printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" "$6" >>"$OUT/tmp/rows.tsv"; }
chk() { row "$1" "$2" "$3" "$4" "$5" "$6"
  case "$6" in PASS) PASS=$((PASS+1));; FAIL) FAIL=$((FAIL+1));; WARN) WARN=$((WARN+1));; *) SKIP=$((SKIP+1));; esac }

# ── 像素工具 ──────────────────────────────────────────────────────────────
AWKLIB='function h2(s,i,hi,lo){if(substr(s,1,1)!="#")i--;hi=index("0123456789abcdef",tolower(substr(s,i,1)))-1;lo=index("0123456789abcdef",tolower(substr(s,i+1,1)))-1;if(hi<0)hi=0;if(lo<0)lo=0;return hi*16+lo}
function dmax(a,b,r,g,bl,m){r=h2(a,2)-h2(b,2);g=h2(a,4)-h2(b,4);bl=h2(a,6)-h2(b,6);if(r<0)r=-r;if(g<0)g=-g;if(bl<0)bl=-bl;m=r;if(g>m)m=g;if(bl>m)m=bl;return m}
function lum(s){return int(0.299*h2(s,2)+0.587*h2(s,4)+0.114*h2(s,6))}'

rowpix() { convert "$1" -crop "$(($4))x1+$3+$2" +repage txt:- 2>/dev/null | tail -n +2 | grep -o '#[0-9A-F]\{6\}' | cut -c1-7; }
colpix() { convert "$1" -crop "1x$(($4))+$2+$3" +repage txt:- 2>/dev/null | tail -n +2 | grep -o '#[0-9A-F]\{6\}' | cut -c1-7; }
px()     { convert "$1" -crop 1x1+"$2"+"$3" txt:- 2>/dev/null | tail -1 | grep -o '#[0-9A-F]\{6\}' | head -1; }
modecol(){ convert "$1" -format %c histogram:info:- 2>/dev/null | sort -rn | head -1 | grep -o '#[0-9A-F]\{6\}' | head -1; }
dmax()   { awk -v a="$1" -v b="$2" "$AWKLIB"'BEGIN{print dmax(a,b)}'; }

expect_l2() { # bg [pct] [fg]
  awk -v bg="$1" -v pct="${2:-$BORDER_L2_PCT}" -v fg="${3:-$INK}" "$AWKLIB"'
    BEGIN{a=pct/100;
      r=int(h2(bg,2)*(1-a)+h2(fg,2)*a+0.5); g=int(h2(bg,4)*(1-a)+h2(fg,4)*a+0.5); b=int(h2(bg,6)*(1-a)+h2(fg,6)*a+0.5)
      printf "#%02X%02X%02X\n", r,g,b}'
}
is_dark_img() { local m l; m=$(modecol "$1"); l=$(awk -v c="$m" "$AWKLIB"'BEGIN{print lum(c)}' 2>/dev/null); [ "${l:-255}" -lt 100 ] && echo 1 || echo 0; }

# 行/列游程中的「线」：等色平台 len<=4 且与左右都差>=5；相邻线合并
# 输出 "from to w color[,color...] leftcolor rightcolor plateauWidth"
LINES_AWK="$AWKLIB"'
{ c=$1; x=o+NR-1
  if(NR==1){ps=x;pe=x;pc=c;pl=1}
  else if(c==pc){pe=x;pl++}
  else {n++; S[n]=ps; E[n]=pe; C[n]=pc; L[n]=pl; ps=x;pe=x;pc=c;pl=1} }
END{ n++; S[n]=ps; E[n]=pe; C[n]=pc; L[n]=pl
  m=0
  for(i=1;i<=n;i++) if(L[i]<=4 && i>1 && i<n && dmax(C[i],C[i-1])>=5 && dmax(C[i],C[i+1])>=5){
    m++; LS[m]=S[i]; LE[m]=E[i]; LC[m]=C[i]; LA[m]=C[i-1]; LB[m]=C[i+1]; LL[m]=L[i-1]+L[i+1] }
  if(m==0) exit
  gs=LS[1]; ge=LE[1]; gc=LC[1]; ga=LA[1]; gb=LB[1]; gl=LL[1]
  for(i=2;i<=m;i++){
    if(LS[i]==ge+1){ ge=LE[i]; gc=gc","LC[i]; gb=LB[i]; gl=LL[i]+gl }
    else { printf "%d %d %d %s %s %s %d\n", gs,ge,ge-gs+1,gc,ga,gb,gl; gs=LS[i];ge=LE[i];gc=LC[i];ga=LA[i];gb=LB[i];gl=LL[i] } }
  printf "%d %d %d %s %s %s %d\n", gs,ge,ge-gs+1,gc,ga,gb,gl }'
lines_row() { rowpix "$1" "$2" "$3" "$4" | awk -v o="$3" "$LINES_AWK"; }
lines_col() { colpix "$1" "$2" "$3" "$4" | awk -v o="$3" "$LINES_AWK"; }

# 稳定的竖线：同一 x 在 3 个 y 上都是线，且线两侧平台合计 >=16px
stable_seam() { # file y xfrom xto
  local f="$1" y="$2" a="$3" b="$4" tmp="$OUT/tmp/cand.$$" xa xb w cols la lb lw k n best="" bestn=0
  : >"$tmp"
  local yy
  for yy in $((y-40)) "$y" $((y+40)); do
    lines_row "$f" "$yy" "$a" $((b-a)) | while read -r xa xb w cols la lb lw; do
      [ -n "${xa:-}" ] || continue
      [ "$w" -le 4 ] || continue
      [ "${lw:-0}" -ge 16 ] || continue
      printf '%s|%s|%s|%s\n' "$xa" "$xb" "$w" "$cols" >>"$tmp"
    done
  done
  while IFS='|' read -r k n; do :; done </dev/null
  local counts
  counts=$(cut -d'|' -f1 "$tmp" | sort | uniq -c | sort -rn)
  while read -r n k; do
    [ -n "${k:-}" ] || continue
    if [ "$n" -ge 2 ] && [ "$n" -gt "$bestn" ]; then bestn=$n; best=$k; fi
  done <<<"$counts"
  if [ -n "$best" ]; then awk -F'|' -v x="$best" '$1==x{print $1" "$2" "$3" "$4}' "$tmp" | sort -t' ' -k3,3n | head -1; fi
  rm -f "$tmp"
}

# ── 检查 0 ────────────────────────────────────────────────────────────────
say "== A3 视觉不变量检查 =="
say "快照目录 : $DIR"
say "基线目录 : ${BASELINE:-<none>}"
say "时间     : $(date '+%Y-%m-%d %H:%M:%S')   $(convert -version | head -1 | cut -d' ' -f1-3)"
[ -f "$DIR/manifest.txt" ] && say "manifest : $(grep -c '^  ' "$DIR/manifest.txt") 张 / head=$(grep '^head:' "$DIR/manifest.txt" | cut -d' ' -f2)"
say ""

# ── 检查 1：尺寸 + 基线差异 ──────────────────────────────────────────────
say "[1] 尺寸与基线差异"
DIMCHANGED=0; NPIC=$(ls "$DIR"/*.png 2>/dev/null | wc -l)
if [ -n "$BASELINE" ]; then
  for f in "$DIR"/*.png; do
    [ -f "$f" ] || continue
    n=$(basename "$f"); cur=$(identify -format "%wx%h" "$f" 2>/dev/null)
    if [ ! -f "$BASELINE/$n" ]; then say "  + 新增  $n  $cur"; chk size "$n" 新增文件 "$cur" "-" WARN; continue; fi
    old=$(identify -format "%wx%h" "$BASELINE/$n" 2>/dev/null)
    if [ "$cur" != "$old" ]; then say "  ! 尺寸  $n  $old -> $cur"; chk size "$n" 画布尺寸 "$cur" "$old" FAIL; DIMCHANGED=$((DIMCHANGED+1)); fi
  done
  for b in "$BASELINE"/*.png; do
    [ -f "$b" ] || continue
    n=$(basename "$b"); [ -f "$DIR/$n" ] || { say "  - 缺失  $n"; chk size "$n" 文件缺失 "-" "存在" WARN; }
  done
  [ "$DIMCHANGED" -eq 0 ] && say "  画布尺寸与基线一致（$NPIC 张）"
else
  say "  （未给 --baseline，跳过）"
fi
say ""

# ── 探针表 ────────────────────────────────────────────────────────────────
PROBES=$(cat <<'EOF'
p1-chat.png|seam|680|250|320|sidebar
p1-chat.png|band|1240|||
p1-chat.png|composer|700|||
p1-chat.png|card|433|728||composer
p1-chat.png|column|140|260|300|column
p1-approval.png|seam|600|250|320|sidebar
p1-approval.png|composer|700|||
p1-trajectory.png|seam|600|250|320|sidebar
p2-appearance.png|seam|400|530|620|settings-nav
p2-appearance.png|card|316|72||dialog
p2-files.png|seam|300|250|320|sidebar
p2-files.png|seam|300|1150|1230|inspector
p2-file-preview.png|seam|300|1150|1230|inspector
p2-capabilities-skills.png|seam|600|250|320|sidebar
p2-capabilities-skills.png|card|307|210||capcard
p2-automation.png|seam|600|250|320|sidebar
p2-automation.png|rows|300|160|600|140|tasklist
p2-model-settings.png|seam|400|530|620|settings-nav
p2-model-settings.png|rows|585|230|570|140|modellist
p2-context.png|seam|600|250|320|sidebar
p2-context.png|column|150|250|300|column
p2-markdown-specimen.png|prose|45|40|0|prose
p2-palette.png|card|414|232||palette
narrow/narrow-session.png|seam|500|250|320|sidebar
narrow/narrow-session-inspector.png|seam|400|650|760|inspector
narrow/narrow-session-inspector.png|clear|700|84||header-clearance
narrow/narrow-settings.png|seam|500|270|340|settings-nav
narrow/dark-session.png|seam|500|250|320|sidebar
p2-context-dark.png|seam|600|250|320|sidebar
EOF
)
[ "$LIST" -eq 1 ] && { printf '%s\n' "$PROBES"; exit 0; }

# ── 检查 2：接缝 ──────────────────────────────────────────────────────────
say "[2] 接缝（目标 1px，颜色 = L2 合成；0px 与 2px 都算 FAIL）"
while IFS='|' read -r scr kind p1 p2 p3 label; do
  [ "${kind:-}" = "seam" ] || continue
  f="$DIR/$scr"
  if [ ! -f "$f" ]; then say "  SKIP $scr ($label) 无此文件"; chk seam "$scr" "$label" "-" "1px" SKIP; continue; fi
  bg=$(modecol "$f")
  if [ "$(is_dark_img "$f")" = "1" ]; then EXP=$(expect_l2 "$bg" "$DARK_L2_PCT" "FFFFFF"); else EXP=$(expect_l2 "$bg"); fi
  got=$(stable_seam "$f" "$p1" "$p2" "$p3")
  if [ -z "$got" ]; then
    W=$(identify -format %w "$f")
    cand=$(lines_row "$f" "$p1" 120 $((W-240)) | awk '{printf "%s..%s(w%s) ", $1,$2,$3}' | cut -c1-140)
    say "  FAIL $scr $label  y=$p1  x=$p2..$p3  无接缝线（0px）"
    say "       该行其它线候选：${cand:-无}"
    chk seam "$scr" "$label" "0px" "1px $EXP" FAIL
    continue
  fi
  set -- $got; xa=$1; xb=$2; w=$3; cols=$4; first=${cols%%,*}
  # 期望色取「缝两侧表面各自合成」中更接近的一个（缝可能由任一侧绘制）
  d=$(dmax "$first" "$EXP")
  for srf in $(printf '%s\n' "$got" | awk '{print $5, $6}'); do
    if [ "$(is_dark_img "$f")" = "1" ]; then e2=$(expect_l2 "$srf" "$DARK_L2_PCT" "FFFFFF"); else e2=$(expect_l2 "$srf"); fi
    d2=$(dmax "$first" "$e2"); [ "$d2" -lt "$d" ] && { d=$d2; EXP=$e2; }
  done
  if [ "$w" -eq 1 ] && [ "$d" -le "$SEAM_COLOR_TOL" ]; then
    say "  PASS $scr $label  x=$xa  w=1  $first ≈ L2 $EXP (Δ$d)"; chk seam "$scr" "$label" "x=$xa w=1 $first" "$EXP" PASS
  elif [ "$w" -eq 1 ]; then
    say "  WARN $scr $label  x=$xa  w=1  $first  期望 $EXP（Δ$d 偏色）"; chk seam "$scr" "$label" "x=$xa w=1 $first" "$EXP" WARN
  else
    say "  FAIL $scr $label  x=$xa..$xb  w=$w  多线叠加 [$cols]  期望 1px $EXP"; chk seam "$scr" "$label" "x=$xa..$xb w=$w [$cols]" "1px $EXP" FAIL
  fi
done <<<"$PROBES"
say ""

# ── 检查 3：accent 填充块 ────────────────────────────────────────────────
say "[3] accent 填充块（≥${ACCENT_BLOCK_MIN}² 连通块；规则 ≤2）"
count_accents() {
  convert "$1" -fuzz 12% -fill black +opaque "$2" -fuzz 12% -fill white +opaque black \
    -define connected-components:verbose=true -define connected-components:area-threshold=80 \
    -connected-components 8 null: 2>/dev/null | tail -n +2 \
  | awk -v min="$ACCENT_BLOCK_MIN" '
      { if ($0 !~ /srgb\(255,255,255\)/) next
        bbox=$2
        if (match(bbox,/^[0-9]+x[0-9]+\+[0-9]+\+[0-9]+$/)) {
          split(bbox,p,"x"); w=p[1]+0; split(p[2],q,"+"); h=q[1]+0
          if (w>=min && h>=min) big++; else if (w>=14 && h>=10) small++ } }
      END{ printf "%d %d\n", big+0, small+0 }'
}
for scr in p1-chat.png p1-approval.png p1-trajectory.png p2-context.png p2-automation.png \
           p2-capabilities-skills.png p2-capabilities-connectors.png p2-landing.png \
           p2-memory-settings.png p2-model-settings.png p2-appearance.png p2-insights.png \
           p2-files.png p2-file-preview.png p2-drop.png p2-artifact.png \
           narrow/narrow-session.png narrow/dark-session.png p2-context-dark.png p2-capabilities-skills-dark.png; do
  f="$DIR/$scr"; [ -f "$f" ] || continue
  [ "$(is_dark_img "$f")" = "1" ] && acc="$ACCENT_DARK" || acc="$ACCENT_LIGHT"
  read -r big small <<<"$(count_accents "$f" "$acc")"
  if [ "$big" -le 2 ]; then say "  PASS $scr  $big 块（小面积候选 $small）"; chk accent "$scr" accent填充块 "$big" "≤2" PASS
  else say "  FAIL $scr  $big 块（小面积候选 $small）超预算"; chk accent "$scr" accent填充块 "$big" "≤2" FAIL; fi
done
say ""

# ── 检查 4：卡片圆角 ─────────────────────────────────────────────────────
say "[4] 卡片圆角（外底色 → 首个非底色 offset；允许 $RADII）"
while IFS='|' read -r scr kind px0 py0 _ _ label; do
  [ "${kind:-}" = "card" ] || continue
  f="$DIR/$scr"; [ -f "$f" ] || { say "  SKIP $scr $label"; chk radius "$scr" "$label" "-" "$RADII" SKIP; continue; }
  ob=$(px "$f" $((px0-30)) $((py0+40)))
  [ -n "$ob" ] || { say "  SKIP $scr $label 取不到外底色"; chk radius "$scr" "$label" "-" "$RADII" SKIP; continue; }
  # 自适应阈值 = max(20, 该行最大对比/2)：阴影（Δ<阈值）不参与
  thr=$(rowpix "$f" $((py0+40)) $((px0-16)) 40 | awk -v bg="$ob" "$AWKLIB"'
        {d=dmax($1,bg); if(d>mx)mx=d} END{t=20; if(mx*85/100>t)t=int(mx*85/100); print t}')
  # 左边界：候选里第一个「整列都是边」的 x（排除文字/图标）
  xL=""
  for c in $(rowpix "$f" $((py0+40)) $((px0-12)) 60 | awk -v o=$((px0-12)) -v bg="$ob" -v t="$thr" "$AWKLIB"'{if(dmax($1,bg)>=t)print o+NR-1}'); do
    n1=$(colpix "$f" "$c" $((py0+10)) 70 | awk -v bg="$ob" -v t="$thr" "$AWKLIB"'{if(dmax($1,bg)>=t)n++}END{print n+0}')
    n2=$(colpix "$f" $((c+1)) $((py0+10)) 70 | awk -v bg="$ob" -v t="$thr" "$AWKLIB"'{if(dmax($1,bg)>=t)n++}END{print n+0}')
    [ "${n1:-0}" -ge 49 ] || [ "${n2:-0}" -ge 49 ] && { xL=$c; break; }
  done
  [ -n "$xL" ] || { say "  SKIP $scr $label 无稳定左边界"; chk radius "$scr" "$label" "无左边界" "$RADII" SKIP; continue; }
  # 上边界：候选里第一个「整行都是边」的 y
  yT=""
  for c in $(colpix "$f" $((xL+40)) $((py0-25)) 90 | awk -v o=$((py0-25)) -v bg="$ob" -v t="$thr" "$AWKLIB"'{if(dmax($1,bg)>=t)print o+NR-1}'); do
    n1=$(rowpix "$f" "$c" $((xL+10)) 70 | awk -v bg="$ob" -v t="$thr" "$AWKLIB"'{if(dmax($1,bg)>=t)n++}END{print n+0}')
    n2=$(rowpix "$f" $((c+1)) $((xL+10)) 70 | awk -v bg="$ob" -v t="$thr" "$AWKLIB"'{if(dmax($1,bg)>=t)n++}END{print n+0}')
    [ "${n1:-0}" -ge 49 ] || [ "${n2:-0}" -ge 49 ] && { yT=$c; break; }
  done
  [ -n "$yT" ] || { say "  SKIP $scr $label 无稳定上边界"; chk radius "$scr" "$label" "无上边界" "$RADII" SKIP; continue; }
  # 26x26 角部网格：每行首个 ≥阈值 的列偏移；r = 首个回到「最小偏移」的行号
  grid=$(convert "$f" -crop 26x26+$((xL-2))+$((yT-2)) +repage txt:- 2>/dev/null | tail -n +2 | grep -o '#[0-9A-F]\{6\}' | cut -c1-7)
  eval "$(printf '%s\n' "$grid" | awk -v bg="$ob" -v t="$thr" "$AWKLIB"'
      { k=int((NR-1)/26); i=(NR-1)%26
        if(dmax($1,bg)>=t && !(k in first)) { first[k]=i; if(mn==""||i<mn) mn=i } }
      END{
        r=0
        for(k=1;k<=22;k++) if((k in first) && first[k]==mn) { r=k; break }
        printf "RDX=%d RMN=%d\n", r+0, mn+0 }')"
  r=${RDX:-0}
  ok="FAIL"; for a in $RADII; do [ "$r" -ge $((a-2)) ] && [ "$r" -le $((a+2)) ] && ok="PASS"; done
  if [ "$r" -le 1 ]; then
    say "  WARN $scr $label  左上=($xL,$yT) 阈值 $thr，角部无法拟合（无描边/纯阴影）"
    chk radius "$scr" "$label" "r不确定" "$RADII" WARN
  else
    say "  $ok $scr $label  左上=($xL,$yT)  r≈$r（阈值 $thr）"; chk radius "$scr" "$label" "r≈$r" "$RADII" "$ok"
  fi
done <<<"$PROBES"
say ""

# ── 检查 5：几何 ──────────────────────────────────────────────────────────
say "[5] 关键几何"
geom_band() { # file x
  local band page; band=$(px "$1" "$2" 3); page=$(px "$1" "$2" 460)
  colpix "$1" "$2" 0 220 | awk -v band="$band" -v page="$page" "$AWKLIB"'
    { y=NR-1; if (y>10 && dmax($1,page)<=3 && dmax(band,page)>=3) { run++; if (run==2) {print y-1; exit} } else run=0 }'
}
geom_composer() { # file x
  local f="$1" W H x0 w y0 h prof top bot left right
  W=$(identify -format %w "$f"); H=$(identify -format %h "$f")
  x0=$((W*28/100)); w=$((W*44/100)); y0=$((H*60/100)); h=$((H-8-y0))
  prof=$(convert "$f" -crop "${w}x${h}+${x0}+${y0}" +repage -fuzz 6% -transparent "$(modecol "$f")" \
        -alpha extract -resize "1x${h}!" txt:- 2>/dev/null | tail -n +2 \
        | awk -F'[,:() ]+' -v y0="$y0" '
            {v=$3+0; if(v>200) print y0+NR-1}
            END{}')
  read -r top bot < <(printf '%s\n' "$prof" | awk -v H="$H" '
      { y[NR]=$1 }
      END{ b=0; for(i=1;i<=NR;i++) if(y[i]<H-24 && y[i]>b) b=y[i]
        t=0; for(i=1;i<=NR;i++) if(y[i]>=b-170 && y[i]<=b-90 && y[i]>t) t=y[i]
        print t+0, b+0 }')
  [ "${top:-0}" -gt 0 ] && [ "${bot:-0}" -gt "${top:-0}" ] || { echo "0 0 0 0 0"; return; }
  read -r left right < <(lines_row "$f" $(( (top+bot)/2 )) $((W*25/100)) $((W*75/100)) | awk 'NR==1{a=$1} {b=$2} END{print (a==0?0:a)+0, b+0}')
  echo "$top $bot $((bot-top)) ${left:-0} ${right:-0}"
}
geom_bbox() { # file y h xfrom
  local f="$1" y="$2" h="$3" xfrom="$4" W bg
  W=$(identify -format %w "$f"); bg=$(modecol "$f")
  convert "$f" -crop "$((W-xfrom))x${h}+${xfrom}+${y}" +repage -fuzz 8% -transparent "$bg" -trim -format "%wx%h%X%Y" info: 2>/dev/null
}
geom_rows() { # file x y w h
  local f="$1" x="$2" y="$3" w="$4" h="$5" bg
  bg=$(convert "$f" -crop "${w}x${h}+${x}+${y}" +repage -colorspace gray -resize 1x"${h}"! -format "%[fx:maxima*255]" info: 2>/dev/null)
  convert "$f" -crop "${w}x${h}+${x}+${y}" +repage -colorspace gray -resize 1x"${h}"! txt:- 2>/dev/null | tail -n +2 \
  | awk -F'[,:() ]+' -v bg="${bg%.*}" '
      {v=$3+0; on=(v < bg-3); if(on&&!pre)s=NR-1; if(!on&&pre){n++; B[n]=s} pre=on}
      END{ if(pre){n++;B[n]=s}
        if(n<2){print n+0, 0; exit}
        m=0; for(i=2;i<=n;i++){ d=B[i]-B[i-1]; if(d>=8){m++; D[m]=d} }
        if(m==0){print n,0; exit}
        for(i=1;i<=m;i++) for(j=i+1;j<=m;j++) if(D[j]<D[i]){t=D[i];D[i]=D[j];D[j]=t}
        printf "%d %d\n", n, D[int((m+1)/2)] }'
}
for scr in $(printf '%s\n' "$PROBES" | cut -d'|' -f1 | sort -u); do
  f="$DIR/$scr"; [ -f "$f" ] || continue
  W=$(identify -format %w "$f"); H=$(identify -format %h "$f")
  printed=0
  while IFS='|' read -r s2 kind p1 p2 p3 p4 label; do
    [ "${s2:-}" = "$scr" ] || continue
    case "$kind" in band|composer|column|prose|rows|clear) ;; *) continue ;; esac
    [ "$printed" -eq 0 ] && { say "  $scr (${W}x${H})"; printed=1; }
    case "$kind" in
      band)
        b=$(geom_band "$f" "$p1")
        v=$(awk -v b="${b:-0}" -v e=$BAND_H 'BEGIN{d=b-e;if(d<0)d=-d;print (d<=2)?"PASS":"FAIL"}')
        say "    顶带高     = ${b:-?}px（期望 ${BAND_H}±2）"; chk geom "$scr" 顶带高 "${b:-0}" "$BAND_H" "$v" ;;
      composer)
        read -r t bo hh l r <<<"$(geom_composer "$f" "$p1")"
        v=$(awk -v h="${hh:-0}" 'BEGIN{print (h>=90&&h<=170)?"PASS":"FAIL"}')
        say "    composer   = y$t..$bo h=$hh  x$l..$r  w=$((r-l))"; chk geom "$scr" composer框 "h=$hh x=$l..$r" "h 90..170" "$v" ;;
      column)
        out=$(geom_bbox "$f" "$p1" "$p2" "$p3"); cw=$(echo "${out:-0x0}" | sed 's/x.*//')
        v=$(awk -v w="${cw:-9999}" -v m=$COLUMN_MAX 'BEGIN{print (w<=m)?"PASS":"FAIL"}')
        say "    内容块     = $out（列上限 $COLUMN_MAX）"; chk geom "$scr" 内容块 "$out" "≤$COLUMN_MAX" "$v" ;;
      prose)
        out=$(geom_bbox "$f" "$p1" "$p2" "$p3"); cw=$(echo "${out:-0x0}" | sed 's/x.*//')
        v=$(awk -v w="${cw:-9999}" -v m=$PROSE_MAX 'BEGIN{print (w<=m)?"PASS":"FAIL"}')
        say "    正文块     = $out（ProseMeasure 640，上限 $PROSE_MAX）"; chk geom "$scr" 正文块 "$out" "≤$PROSE_MAX" "$v" ;;
      rows)
        read -r nb pit <<<"$(geom_rows "$f" "$p1" "$p2" "$p3" "$p4")"
        # 信息项：多行列表的"段"是文本行，不是行本身；行归属由清单 F2/F3 人判
        say "    行距采样   = $nb 段 / 中位 ${pit}px（$label，信息项）"
        chk geom "$scr" "行距-$label" "${pit}px/$nb" "信息项" WARN ;;
      clear)
        seam=$p1; bh=$p2
        # 不变量：顶带内最后一个墨点距接缝 >= 8px（允许 ⋯ 溢出按钮在带内，只禁止"贴缝/被裁"）
        bandbg=$(px "$f" $((seam+20)) 20)
        dist=$(convert "$f" -crop "64x${bh}+$((seam-64))+0" +repage txt:- 2>/dev/null | tail -n +2 \
          | awk -F'[,:#() ]+' -v bg="$bandbg" -v x0=$((seam-64)) "$AWKLIB"'
              { x=$1; if (dmax($4,bg)>=40 && x>last) last=x }
              END{ if (last=="") print 999; else print last }')
        gap=$(( seam - dist ))
        [ "$dist" -eq 999 ] && gap=999
        v=$(awk -v g="$gap" 'BEGIN{print (g>=8)?"PASS":"FAIL"}')
        say "    顶带留白   = 最后一个墨点距接缝 ${gap}px（≥8 = 未被裁/未贴缝）"; chk geom "$scr" 顶带留白 "gap=${gap}px" "≥8px" "$v" ;;
    esac
  done <<<"$PROBES"
done
say ""

# ── 检查 6：暗色 ramp ────────────────────────────────────────────────────
say "[6] 暗色表面 ramp（导航必须比页面深）"
for scr in p2-context-dark.png narrow/dark-session.png p2-capabilities-skills-dark.png p2-insights-dark.png; do
  f="$DIR/$scr"; [ -f "$f" ] || continue
  nav=$(px "$f" 150 500); page=$(px "$f" 700 400)
  nl=$(awk -v c="$nav" "$AWKLIB"'BEGIN{print lum(c)}'); pl=$(awk -v c="$page" "$AWKLIB"'BEGIN{print lum(c)}')
  if [ "${nl:-0}" -lt "${pl:-0}" ]; then say "  PASS $scr  nav=$nav(L$nl) < page=$page(L$pl)"; chk ramp "$scr" "nav<page" "L$nl<L$pl" "nav 更暗" PASS
  else say "  FAIL $scr  nav=$nav(L$nl) >= page=$page(L$pl)"; chk ramp "$scr" "nav<page" "L$nl>=L$pl" "nav 更暗" FAIL; fi
done
say ""

# ── 检查 7：manifest 差异 ───────────────────────────────────────────────
if [ -f "$DIR/manifest.txt" ] && [ -n "$BASELINE" ] && [ -f "$BASELINE/manifest.txt" ]; then
  say "[7] manifest 差异"
  a=$(grep '^  ' "$DIR/manifest.txt" | sed 's/^ *//' | sort); b=$(grep '^  ' "$BASELINE/manifest.txt" | sed 's/^ *//' | sort)
  add=$(comm -23 <(echo "$a") <(echo "$b")); del=$(comm -13 <(echo "$a") <(echo "$b"))
  [ -n "$add" ] && say "  新增：$(echo "$add" | tr '\n' ' ')"
  [ -n "$del" ] && say "  消失：$(echo "$del" | tr '\n' ' ')"
  [ -z "$add$del" ] && say "  场景清单一致"
  say ""
fi

# ── 汇总 ─────────────────────────────────────────────────────────────────
say "== 汇总 =="
say "  PASS=$PASS  FAIL=$FAIL  WARN=$WARN  SKIP=$SKIP"
cp "$OUT/tmp/rows.tsv" "$TSV"
say "  机读明细：$TSV"
if [ "$FAIL" -gt 0 ]; then
  say "  失败项："
  awk -F'\t' '$6=="FAIL"{printf "   - [%s] %s：%s = %s（期望 %s）\n",$1,$2,$3,$4,$5}' "$TSV" | tee -a "$REPORT"
fi
say ""
say "报告：$REPORT"
[ "$STRICT" -eq 1 ] && [ "$FAIL" -gt 0 ] && exit 1
exit 0