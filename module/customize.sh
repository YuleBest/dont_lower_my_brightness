#!/system/bin/sh
# shellcheck shell=ash
# 刷入时读设备自己的 display_brightness_app_list.xml，把其中 <app> 元素整个删除，
# 结果写进模块的 system/ 目录，由 overlay 挂载覆盖 /my_product 下的原文件。
#
# 必须是**删除元素**而不是把内容掏空：留下 <app></app> 会让系统把空字符串当包名
# 去解析，导致无法开机。处理逻辑见 golang/main.go。
#
# 之所以在刷入时生成而非预置一份：各机型的应用列表与亮度参数（nit / ratio /
# rate）都不同，预置的会绑死在某一个机型上。删除动作本身与机型无关。

SRC_DEVICE=${BRIGHTNESS_XML_SRC:-/my_product/vendor/etc/display_brightness_app_list.xml}
DEST_DIR=$MODPATH/system/my_product/vendor/etc
DEST=$DEST_DIR/display_brightness_app_list.xml
BACKUP=$MODPATH/backup/display_brightness_app_list.xml

case "$ARCH" in
	arm64) TOOL=$MODPATH/bin/brightness-xml-arm64 ;;
	arm) TOOL=$MODPATH/bin/brightness-xml-arm ;;
	*) abort "! 不支持的架构: $ARCH（本模块仅提供 arm64 / arm）" ;;
esac

[ -f "$TOOL" ] || abort "! 模块缺少 $ARCH 架构的工具: $TOOL"
# 安装器的 unzip 未必还原 zip 里的可执行位，这里显式补上再执行
chmod 0755 "$TOOL" 2>/dev/null

# --- 选源文件 ---
# 优先用上次刷入留下的备份：模块生效期间 /my_product 下的原文件被 overlay 遮住，
# 直接读设备只会拿到已被本模块处理过的版本（<app> 已删光），拿它既做不出正确的
# payload，也会把唯一的原始副本覆盖掉。
#
# 本次安装写入 $MODPATH（<NVBASE>/modules_update/<id>），而生效中的模块在
# <NVBASE>/modules/<id>，两者不是同一个目录，所以更新时上次的备份仍在原处。
MODULE_ID=${MODPATH##*/}
NVBASE=${MODPATH%/*/*}
PREV_BACKUP=$NVBASE/modules/$MODULE_ID/backup/display_brightness_app_list.xml

if [ -n "$BRIGHTNESS_XML_SRC" ]; then
	# 显式指定的路径（也是测试用的注入点），优先级最高
	SRC=$BRIGHTNESS_XML_SRC
	ui_print "  使用指定的源文件: $SRC"
elif [ -f "$PREV_BACKUP" ]; then
	SRC=$PREV_BACKUP
	ui_print "  使用上次刷入的备份作为源文件"
else
	SRC=$SRC_DEVICE
fi
[ -f "$SRC" ] || abort "! 找不到源文件 $SRC，本模块不适用该机型"

# --- 备份原文件 ---
# 放在模块根目录下而不是 system/ 里，因此不会被挂载到系统、也不会被挂载覆盖。
mkdir -p "${BACKUP%/*}"
if grep -q -E '<app[ />]' "$SRC"; then
	cp -f "$SRC" "$BACKUP" || abort "! 备份 $SRC 失败"
	ui_print "  原文件已备份到 $BACKUP"
elif [ -f "$BACKUP" ]; then
	ui_print "  沿用已有备份: $BACKUP"
else
	# 拿到的源里没有 <app>，又没有备份可用，说明原始文件已无从获取
	abort "! $SRC 中已无 <app> 元素且没有原始备份；若本模块此前已刷入，请先卸载模块并重启再刷入"
fi

# --- 生成 payload ---
# 先写到临时文件，成功后再就位：失败时不留空目录或半个文件
TMP_OUT=$MODPATH/.display_brightness_app_list.xml.tmp
if ! _msg=$("$TOOL" -o "$TMP_OUT" "$SRC" 2>&1); then
	rm -f "$TMP_OUT"
	abort "! 处理 $SRC 失败: $_msg"
fi
if [ ! -s "$TMP_OUT" ]; then
	rm -f "$TMP_OUT"
	abort "! 生成结果为空: $SRC"
fi
# 自检兜底：payload 里绝不能残留 <app>。工具内部已做同样检查，这里再挡一道 ——
# 空元素会被系统当作空包名解析并导致无法开机，这个代价太大，不值得只依赖一处。
if grep -q -E '<app[ />]' "$TMP_OUT"; then
	rm -f "$TMP_OUT"
	abort "! 生成的配置中仍残留 <app> 元素，已放弃写入（残留会导致无法开机）"
fi
mkdir -p "$DEST_DIR"
mv -f "$TMP_OUT" "$DEST"
ui_print "  $_msg"

# --- 权限 ---
set_perm_recursive "$MODPATH" 0 0 0755 0644
if [ -d "$MODPATH/bin" ]; then
	set_perm_recursive "$MODPATH/bin" 0 0 0755 0755
fi
for _f in post-fs-data.sh post-mount.sh service.sh boot-completed.sh \
	uninstall.sh action.sh late-load.sh; do
	if [ -f "$MODPATH/$_f" ]; then
		set_perm "$MODPATH/$_f" 0 0 0755
	fi
done

# 本脚本被安装器 source 执行，退出码非零会让带 set -e 的安装流程中断。
# 上面的循环若写成 `[ -f x ] && cmd`，在最后一个文件不存在时会返回非零，
# 因此这里改用 if 判断，并用 `:` 收尾确保返回 0（不能写 exit，会中断安装）。
:
