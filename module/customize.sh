#!/system/bin/sh
# shellcheck shell=ash

SRC_DEVICE=${BRIGHTNESS_XML_SRC:-/my_product/vendor/etc/display_brightness_app_list.xml}
DEST_DIR=$MODPATH/system/my_product/vendor/etc
DEST=$DEST_DIR/display_brightness_app_list.xml
BACKUP=$MODPATH/backup/display_brightness_app_list.xml

case "$ARCH" in
    arm64) TOOL=$MODPATH/bin/brightness-xml-arm64 ;;
    *) abort "! 不支持的架构: $ARCH（本模块仅提供 arm64）" ;;
esac

[ -f "$TOOL" ] || abort "! 模块缺少 $ARCH 架构的工具: $TOOL"
chmod 0755 "$TOOL" 2>/dev/null

MODULE_ID=${MODPATH##*/}
NVBASE=${MODPATH%/*/*}
PREV_BACKUP=$NVBASE/modules/$MODULE_ID/backup/display_brightness_app_list.xml

if [ -n "${BRIGHTNESS_XML_SRC:-}" ]; then
    SRC=$BRIGHTNESS_XML_SRC
    ui_print "  使用指定的源文件: $SRC"
elif [ -f "$PREV_BACKUP" ]; then
    SRC=$PREV_BACKUP
    ui_print "  使用上次刷入的备份作为源文件"
else
    SRC=$SRC_DEVICE
fi
[ -f "$SRC" ] || abort "! 找不到源文件 $SRC，本模块不适用该机型"

mkdir -p "${BACKUP%/*}"
if grep -q -E '<app[ />]' "$SRC"; then
    cp -f "$SRC" "$BACKUP" || abort "! 备份 $SRC 失败"
    ui_print "  原文件已备份到 $BACKUP"
elif [ -f "$BACKUP" ]; then
    ui_print "  沿用已有备份: $BACKUP"
else
    abort "! $SRC 中已无 <app> 元素且没有原始备份；若本模块此前已刷入，请先卸载模块并重启再刷入"
fi

TMP_OUT=$MODPATH/.display_brightness_app_list.xml.tmp
if ! _msg=$("$TOOL" -o "$TMP_OUT" "$SRC" 2>&1); then
    rm -f "$TMP_OUT"
    abort "! 处理 $SRC 失败: $_msg"
fi
if [ ! -s "$TMP_OUT" ]; then
    rm -f "$TMP_OUT"
    abort "! 生成结果为空: $SRC"
fi
if grep -q -E '<app[ />]' "$TMP_OUT"; then
    rm -f "$TMP_OUT"
    abort "! 生成的配置中仍残留 <app> 元素，已放弃写入"
fi
mkdir -p "$DEST_DIR"
mv -f "$TMP_OUT" "$DEST"
ui_print "  $_msg"

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

:
