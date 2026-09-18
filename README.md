# 不要降亮度

禁止 ColorOS 在特定应用中自动降低屏幕亮度

ColorOS 会依据 `/my_product/vendor/etc/display_brightness_app_list.xml` 里的应用列表主动下调屏幕亮度。本模块在**刷入时**读取设备配置，把列表清空后挂载覆盖回去，系统匹配不到任何应用，亮度就不再被下调。

本模块仅会禁止 ColorOS 根据应用列表下调亮度，不会影响系统的其他亮度调节逻辑（如自动亮度、护眼模式、夜间模式、温控等）。

## 它做了什么

1. 刷入时读取设备上的 `/my_product/vendor/etc/display_brightness_app_list.xml`
2. 先把原文件备份到模块的 `backup/` 目录
3. 把其中每个 `<app>` 删掉，把结果写回模块目录
4. 模块挂载覆盖掉系统原文件

## 安装

- 去 [Release](https://github.com/YuleBest/dont_lower_my_brightness/releases) 页面下载最新版本，使用 Root 管理器刷入即可

- 你也可以自己构建模块：

  ```bash
  git clone https://github.com/YuleBest/dont_lower_my_brightness.git
  cd dont_lower_my_brightness
  bash ./build.sh  # → dist/dont_lower_my_brightness_v0.1.0.zip
  ```

## 项目结构

```
├── golang/            XML 处理工具
├── module/            模块源码
│   ├── customize.sh   刷入时读设备配置并生成 payload
│   ├── bin/           构建期放入交叉编译产物
│   ├── backup/        原文件备份（刷入时生成）
│   └── system/        payload 落点（刷入时生成，源码树里只有占位文件）
├── reference/         参考 XML
├── meta.json          模块身份信息
├── update.json        版本号唯一来源
└── build.sh           唯一构建入口
```

## 适用性

模块在刷入时若找不到 `/my_product/vendor/etc/display_brightness_app_list.xml` 会直接中止安装并提示，路径是 ColorOS / realme UI 特有的。

## Q & A

- **刷入成功但亮度照旧被下调**：先确认设备装了提供挂载能力的元模块
- **确认文件已生效**：刷入后查看 `/my_product/vendor/etc/display_brightness_app_list.xml`，`<app>` 应为空
- **想保留部分应用**：本模块的取舍是全清空。若要白名单式保留，可自行修改 `golang/main.go` 的处理逻辑
