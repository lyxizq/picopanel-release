#!/bin/sh
# 组发布通道目录（TODO 404，GitHub Releases 单源）：拷贝 make release 产物三包、
# 算 sha256/size、以当前 tag 注释为更新说明生成 manifest.json，ed25519 签名并就地自验。
# 用法（由 Makefile 的 channel 目标调用，须在仓库根执行）:
#   GO=go deploy/channel.sh <tag> <channel> <outdir> <私钥路径> <公钥路径>
# 产物：outdir 下 5 个上传资产（三 .gz + manifest.json + manifest.json.sig）
#       + notes.txt（仅本地，作 gh release 的 --notes-file，不上传）。
set -eu

TAG=${1:?缺 tag}
CH=${2:?缺 channel}
OUT=${3:?缺输出目录}
KEY=${4:?缺私钥路径}
PUB=${5:?缺公钥路径}
GO=${GO:-go}

for f in release/deploy/server/lz-server.gz \
	release/deploy/agent-amd64/lz-agent.gz \
	release/deploy/agent-arm64/lz-agent.gz; do
	[ -f "$f" ] || { echo "错误: 缺 $f（先 make release）" >&2; exit 1; }
done

mkdir -p "$OUT"
cp release/deploy/server/lz-server.gz "$OUT/lz-server.gz"
cp release/deploy/agent-amd64/lz-agent.gz "$OUT/lz-agent-amd64.gz"
cp release/deploy/agent-arm64/lz-agent.gz "$OUT/lz-agent-arm64.gz"

sha256() {
	if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
	else sha256sum "$1" | awk '{print $1}'; fi
}
filesize() { stat -f %z "$1" 2>/dev/null || stat -c %s "$1"; }

SV_SHA=$(sha256 "$OUT/lz-server.gz");      SV_SIZE=$(filesize "$OUT/lz-server.gz")
AM_SHA=$(sha256 "$OUT/lz-agent-amd64.gz"); AM_SIZE=$(filesize "$OUT/lz-agent-amd64.gz")
AR_SHA=$(sha256 "$OUT/lz-agent-arm64.gz"); AR_SIZE=$(filesize "$OUT/lz-agent-arm64.gz")
PUBLISHED=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# 更新日志 = tag 注释（annotated tag 的 message）：原文进 notes.txt 供 GitHub Release
# 展示，JSON 转义后进 manifest.notes 供客户端更新弹窗展示
NOTES=$(git tag -l --format='%(contents)' "$TAG" | perl -0777 -pe 's/\s+\z/\n/')
[ -n "$(printf '%s' "$NOTES" | tr -d '[:space:]')" ] || {
	echo "错误: tag $TAG 无注释——更新日志取自 tag message，请用 git tag -a vX.Y.Z -m \"更新日志\" 重打" >&2
	exit 1
}
NOTES_JSON=$(printf '%s' "$NOTES" | perl -0777 -pe 's/\\/\\\\/g; s/"/\\"/g; s/\r/\\r/g; s/\t/\\t/g; s/\n/\\n/g')

cat > "$OUT/manifest.json" <<EOF
{
  "schema": 1,
  "version": "$TAG",
  "channel": "$CH",
  "published_at": "$PUBLISHED",
  "notes": "$NOTES_JSON",
  "assets": {
    "server":      { "file": "lz-server.gz",      "sha256": "$SV_SHA", "size": $SV_SIZE },
    "agent_amd64": { "file": "lz-agent-amd64.gz", "sha256": "$AM_SHA", "size": $AM_SIZE },
    "agent_arm64": { "file": "lz-agent-arm64.gz", "sha256": "$AR_SHA", "size": $AR_SIZE }
  }
}
EOF

"$GO" run ./cmd/lz-sign sign -key "$KEY" -i "$OUT/manifest.json" -s "$OUT/manifest.json.sig" >/dev/null
"$GO" run ./cmd/lz-sign verify -pub "$PUB" -i "$OUT/manifest.json" -s "$OUT/manifest.json.sig" >/dev/null

printf '%s\n' "$NOTES" > "$OUT/notes.txt"

echo "=== channel 就绪: ${OUT}（${TAG} / ${CH}，签名已自验）==="
for f in lz-server.gz lz-agent-amd64.gz lz-agent-arm64.gz manifest.json manifest.json.sig; do
	printf '  %-20s %10s B  %s…\n' "$f" "$(filesize "$OUT/$f")" "$(sha256 "$OUT/$f" | cut -c1-16)"
done
