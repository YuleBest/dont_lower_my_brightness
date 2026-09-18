#!/bin/sh
# 构建脚本：把源码组装成可刷入的模块 zip
#
# 数据流（详见《模块项目目录规范》）：
#   module/                  --原样复制-->  dist/build/
#   webui/                   --构建------>  dist/build/webroot/
#   <语言>/                  --构建------>  dist/build/<按需定义>/
#   meta.json + update.json  --生成------>  dist/build/module.prop
#   dist/build/              --打包------>  dist/<id>_<version>.zip
#
# 用法: ./build.sh
set -eu
cd "$(dirname "$0")"
ROOT=$PWD
BUILD=$ROOT/dist/build
CACHE=$ROOT/dist/cache

ID=$(python3 -c "import json;print(json.load(open('meta.json'))['id'])")
VERSION=$(python3 -c "import json;print(json.load(open('update.json'))['version'])")
OUT="$ROOT/dist/${ID}_${VERSION}.zip"

# ---- 1. 清空组装区（保留缓存）----
rm -rf "$BUILD"
mkdir -p "$BUILD" "$CACHE"

# ---- 2. module/ 原样复制 ----
# 这里是唯一进入 zip 的源码目录；生成物一律放 dist/build/，不要写回 module/
cp -a "$ROOT/module/." "$BUILD/"

# ---- 3. webui/ 构建到 dist/build/webroot ----
# 两种情况：
#   a) webui/ 是纯静态页面（无 package.json）——直接复制过去
#   b) webui/ 有 package.json（前端工程）——需在此填入真实构建命令，例如：
#        pnpm -C "$ROOT/webui" install --frozen-lockfile
#        pnpm -C "$ROOT/webui" build     # 其 outDir 指向 ../dist/build/webroot
#
# 注意：不要留下空的 webroot/ —— 空目录会进 zip，模块带空 WebUI 比不带更糟
# （管理器会显示入口，点开却是空白）。
if [ -d "$ROOT/webui" ]; then
    if [ -f "$ROOT/webui/package.json" ]; then
        echo "错误: webui/ 是前端工程，尚未接入构建命令。" >&2
        echo "      请在 build.sh 步骤 3 填入构建命令（并确保产物输出到 dist/build/webroot）。" >&2
        exit 1
    fi
    mkdir -p "$BUILD/webroot"
    cp -a "$ROOT/webui/." "$BUILD/webroot/"
    rm -f "$BUILD/webroot/README.md"
fi

# ---- 4. 写入 META-INF（recovery / Magisk 刷入所需）----
# 这两个文件内容固定，不要修改：
#   update-binary 是 Magisk 官方的安装器入口，recovery 刷入时由它调用 install_module
#   updater-script 只含 "#MAGISK" 标记，告诉 recovery 这是 Magisk 模块
# KernelSU 会用 -x 'META-INF/*' 排除掉它们（所以对 KernelSU 无影响），
# 但 Magisk / recovery 刷入时缺了会直接失败。
META_DIR="$BUILD/META-INF/com/google/android"
mkdir -p "$META_DIR"

cat >"$META_DIR/update-binary" <<'UPDATE_BINARY_EOF'
#!/sbin/sh

#################
# Initialization
#################

umask 022

# echo before loading util_functions
ui_print() { echo "$1"; }

require_new_magisk() {
  ui_print "*******************************"
  ui_print " Please install Magisk v20.4+! "
  ui_print "*******************************"
  exit 1
}

#########################
# Load util_functions.sh
#########################

OUTFD=$2
ZIPFILE=$3

mount /data 2>/dev/null

[ -f /data/adb/magisk/util_functions.sh ] || require_new_magisk
. /data/adb/magisk/util_functions.sh
[ $MAGISK_VER_CODE -lt 20400 ] && require_new_magisk

install_module
exit 0
UPDATE_BINARY_EOF

printf '#MAGISK\n' >"$META_DIR/updater-script"

# update-binary 由 recovery 直接执行，必须有可执行位
chmod 755 "$META_DIR/update-binary"
chmod 644 "$META_DIR/updater-script"

# ---- 5. golang/ 交叉编译 ----
mkdir -p "$BUILD/bin"

TOOL_BIN=$BUILD/bin/brightness-xml-arm64
echo "编译 golang/ -> bin/brightness-xml-arm64 (arm64)"
(
    cd "$ROOT/golang"
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -trimpath -buildvcs=false \
        -ldflags='-s -w -buildid=' \
        -o "$TOOL_BIN" .
)

if ! command -v upx >/dev/null 2>&1; then
    echo "错误: 未找到 upx，无法压缩 bin/brightness-xml-arm64" >&2
    echo "      Debian/Ubuntu: apt-get install upx-ucl" >&2
    exit 1
fi
echo "upx -9 压缩 bin/brightness-xml-arm64"
upx -9 --quiet "$TOOL_BIN"

# ---- 6. 生成 module.prop（由 meta.json + update.json 合成，LF 换行）----
python3 - "$ROOT" <<'PYEOF'
import json, pathlib, sys

root = pathlib.Path(sys.argv[1])
meta = json.loads((root / "meta.json").read_text(encoding="utf-8"))
upd = json.loads((root / "update.json").read_text(encoding="utf-8"))

# 字段顺序与 KernelSU 文档一致；version/versionCode 取自 update.json，
# 其余取自 meta.json，因此版本号只在一个文件里维护。
order = ["id", "name", "version", "versionCode", "author", "description",
         "updateJson", "actionIcon", "webuiIcon"]
lines = []
for key in order:
    value = upd.get(key, meta.get(key))
    if value is None:
        continue
    lines.append(f"{key}={value}")

root.joinpath("dist", "build", "module.prop").write_text(
    "\n".join(lines) + "\n", encoding="utf-8", newline="\n"
)
PYEOF

# ---- 7. 校验 update.json 的 zipUrl 与实际产物一致 ----
# zipUrl 里的文件名是手写的，写错会导致更新下载失败且无任何报错，
# 只有构建期能拦住。
python3 - "$ROOT" "$ID" "$VERSION" <<'PYEOF'
import json, pathlib, sys

root = pathlib.Path(sys.argv[1])
mod_id, version = sys.argv[2], sys.argv[3]
upd = json.loads((root / "update.json").read_text(encoding="utf-8"))

want = upd["zipUrl"].rsplit("/", 1)[-1]
expect = f"{mod_id}_{version}.zip"
if want != expect:
    sys.exit(f"错误: update.json 的 zipUrl 文件名是 {want}, 而实际产物是 {expect}")
PYEOF

# ---- 8. 打包 ----
# 用 Python 的 zipfile 而非 zip(1)：可以固定条目时间戳，让同样内容产出逐字节
# 相同的 zip（便于比对与校验）。zip(1) 会写入文件 mtime，而 module.prop 每次
# 构建都是新生成的，导致产物每次都不同。
python3 - "$BUILD" "$OUT" <<'PYEOF'
import os, pathlib, stat, sys, zipfile

src, out = pathlib.Path(sys.argv[1]), sys.argv[2]

# 固定时间戳（1980-01-01 是 zip 格式的最小合法值）
FIXED = (1980, 1, 1, 0, 0, 0)
EXEC = (stat.S_IFREG | 0o755) << 16
FILE = (stat.S_IFREG | 0o644) << 16

# bin/ 下的文件没有后缀，靠名字识别
TOOLS = "brightness-xml"

entries = []
for root, dirs, names in os.walk(src):
    rel_root = pathlib.Path(root).relative_to(src)
    dirs[:] = sorted(d for d in dirs if d != "__pycache__")
    for name in sorted(names):
        # .placeholder 只是让空目录能被 git 保留，不该进模块
        if name == ".placeholder" or ".git" in name:
            continue
        entries.append(rel_root / name)

pathlib.Path(out).unlink(missing_ok=True)
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
    for rel in entries:
        path = src / rel
        info = zipfile.ZipInfo(str(rel).replace(os.sep, "/"))
        info.date_time = FIXED
        info.compress_type = zipfile.ZIP_DEFLATED
        # 保留可执行位：设备端靠它判断脚本/工具能否直接执行
        is_exec = bool(path.stat().st_mode & stat.S_IXUSR) and (
            path.suffix in (".sh", "") or path.name in ("busybox",)
            or path.name.startswith(TOOLS + "-")
        )
        info.external_attr = EXEC if is_exec else FILE
        zf.writestr(info, path.read_bytes())
PYEOF

# ---- 9. 报告 ----
echo "产出: $OUT ($(wc -c <"$OUT") 字节)"
