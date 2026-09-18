package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample 是一份与设备上同构的小样本：XML 声明、注释、缩进、method 参数、
// 以及 method 外的 <app> 都在。行尾都带换行，与真实文件一致。
const sample = `<?xml version="1.0" encoding="UTF-8"?>
<!-- 这个文件会位于 /my_product/vendor/etc/display_brightness_app_list.xml -->
<root>
    <version>20251212</version>

    <!--Expressiveness App Reduction v1.5-->
    <method id="0">
        <!-- 浏览器 -->
        <app>com.android.chrome</app>
        <!-- 钱包 -->
        <app>com.finshell.wallet</app>
        <switch>1</switch>
        <nit mode="0">70,130,380,500</nit>
    </method>

    <global_brightness_limit nit="1200">
        <app>com.android.launcher</app>
    </global_brightness_limit>

    <app_reduce_30hz_on_support>1</app_reduce_30hz_on_support>
</root>
`

// 期望结果：<app> 整行（含其上方说明注释）被删除，其余一字不差。
const sampleStripped = `<?xml version="1.0" encoding="UTF-8"?>
<!-- 这个文件会位于 /my_product/vendor/etc/display_brightness_app_list.xml -->
<root>
    <version>20251212</version>

    <!--Expressiveness App Reduction v1.5-->
    <method id="0">
        <switch>1</switch>
        <nit mode="0">70,130,380,500</nit>
    </method>

    <global_brightness_limit nit="1200">
    </global_brightness_limit>

    <app_reduce_30hz_on_support>1</app_reduce_30hz_on_support>
</root>
`

// hasAppElement 判断内容里是否还有 <app> 元素。独立于生产代码实现，避免
// 用被测逻辑验证被测结果。注意不能简单匹配 "<app" —— 文件里还有
// <app_reduce_30hz_on_support> 这类同前缀元素。
func hasAppElement(s string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], "<app")
		if j < 0 {
			return false
		}
		k := i + j + len("<app")
		if k >= len(s) {
			return false
		}
		switch c := s[k]; {
		case c == '_' || c == '-' || c == '.' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			i = k // 名字的延续，是别的元素
		default:
			return true
		}
	}
}

func TestRemoveApps(t *testing.T) {
	out, n, err := removeApps([]byte(sample), scopeAll, false)
	if err != nil {
		t.Fatalf("removeApps 返回错误: %v", err)
	}
	if n != 3 {
		t.Errorf("删除数量 = %d，期望 3", n)
	}
	if string(out) != sampleStripped {
		t.Errorf("<app> 以外的内容被改动\n--- 实际 ---\n%s\n--- 期望 ---\n%s", out, sampleStripped)
	}
	// 关键回归：绝不能留下空元素，空包名会导致无法开机
	if hasAppElement(string(out)) {
		t.Errorf("输出中仍残留 <app> 元素，会导致无法开机\n%s", out)
	}
	// 名字以 app 开头的其他元素必须保留
	if !strings.Contains(string(out), `<app_reduce_30hz_on_support>1</app_reduce_30hz_on_support>`) {
		t.Error("<app_reduce_30hz_on_support> 被误删")
	}
}

func TestRemoveAppsScopeMethod(t *testing.T) {
	out, n, err := removeApps([]byte(sample), scopeMethod, false)
	if err != nil {
		t.Fatalf("removeApps 返回错误: %v", err)
	}
	if n != 2 {
		t.Errorf("删除数量 = %d，期望 2", n)
	}
	got := string(out)
	// method 外的 <app> 不在范围内，必须原样保留
	if !strings.Contains(got, "        <app>com.android.launcher</app>\n") {
		t.Errorf("method 外的 <app> 不应被删除\n---\n%s", got)
	}
	for _, gone := range []string{"com.android.chrome", "com.finshell.wallet"} {
		if strings.Contains(got, gone) {
			t.Errorf("method 内的 %q 应被删除\n---\n%s", gone, got)
		}
	}
}

// 关掉注释删除时，说明注释要保留，只有 <app> 行被删。
func TestRemoveAppsKeepComments(t *testing.T) {
	out, n, err := removeApps([]byte(sample), scopeAll, true)
	if err != nil {
		t.Fatalf("removeApps 返回错误: %v", err)
	}
	if n != 3 {
		t.Errorf("删除数量 = %d，期望 3", n)
	}
	got := string(out)
	for _, want := range []string{"<!-- 浏览器 -->", "<!-- 钱包 -->"} {
		if !strings.Contains(got, want) {
			t.Errorf("应保留注释 %q\n---\n%s", want, got)
		}
	}
	if hasAppElement(got) {
		t.Errorf("仍残留 <app> 元素\n%s", got)
	}
}

// 重复处理应当稳定：<app> 已删光，再跑一次没有任何可删的。
func TestRemoveAppsIdempotent(t *testing.T) {
	once, _, err := removeApps([]byte(sample), scopeAll, false)
	if err != nil {
		t.Fatalf("removeApps 返回错误: %v", err)
	}
	twice, n, err := removeApps(once, scopeAll, false)
	if err != nil {
		t.Fatalf("二次 removeApps 返回错误: %v", err)
	}
	if n != 0 {
		t.Errorf("二次删除数量 = %d，期望 0", n)
	}
	if string(once) != string(twice) {
		t.Error("重复处理的结果不一致")
	}
}

// 真实文件的处理结果：<app> 全部消失，且不得出现空元素。
func TestRemoveAppsReferenceFile(t *testing.T) {
	path := filepath.Join("..", "reference", "display_brightness_app_list.xml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("参考文件不可读（%v），跳过", err)
	}

	out, n, err := removeApps(data, scopeAll, false)
	if err != nil {
		t.Fatalf("removeApps 返回错误: %v", err)
	}
	if n == 0 {
		t.Fatal("参考文件中未删除任何 <app>")
	}

	got := string(out)
	if hasAppElement(got) {
		t.Error("输出中仍残留 <app> 元素")
	}
	if strings.Contains(got, "com.") {
		t.Error("输出中仍残留包名")
	}
	// 关键参数必须原样保留
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<version>20251212</version>",
		`<nit mode="0">70,130,380,500</nit>`,
		"<dolby_temperature_limit_nit>",
		"<game_edr>",
		"<camera_limit_nit>",
		`<app_reduce_30hz_on_support>1</app_reduce_30hz_on_support>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("输出丢失了 %q", want)
		}
	}
	t.Logf("删除了 %d 个 <app>，%d → %d 字节", n, len(data), len(out))
}

func TestRemoveAppsEdgeCases(t *testing.T) {
	t.Run("同一行内的 app", func(t *testing.T) {
		// 元素不独占整行时不能吃掉同一行的其他内容
		out, n, err := removeApps([]byte(`<root><method id="0"><app>x</app></method></root>`), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != `<root><method id="0"></method></root>` {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("自闭合", func(t *testing.T) {
		out, n, err := removeApps([]byte("<root>\n  <app/>\n</root>\n"), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != "<root>\n</root>\n" {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("无 app", func(t *testing.T) {
		_, n, err := removeApps([]byte(`<root><version>1</version></root>`), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 0 {
			t.Errorf("n=%d，期望 0", n)
		}
	})

	t.Run("未闭合", func(t *testing.T) {
		if _, _, err := removeApps([]byte(`<root><app>x`), scopeAll, false); err == nil {
			t.Error("未闭合的 <app> 应当报错")
		}
	})

	t.Run("末行无换行", func(t *testing.T) {
		out, n, err := removeApps([]byte("<root>\n<app>x</app>"), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != "<root>\n" {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("CRLF", func(t *testing.T) {
		out, n, err := removeApps([]byte("<root>\r\n<app>x</app>\r\n</root>\r\n"), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != "<root>\r\n</root>\r\n" {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("连续多个 app", func(t *testing.T) {
		in := "<root>\n<app>a</app>\n<app>b</app>\n</root>\n"
		out, n, err := removeApps([]byte(in), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 2 || string(out) != "<root>\n</root>\n" {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("跨行注释不被吞掉", func(t *testing.T) {
		// 上方是多行注释时只删元素本身，不能误删注释里的内容
		in := "<root>\n<!-- 第一行\n第二行 -->\n<app>x</app>\n</root>\n"
		out, n, err := removeApps([]byte(in), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != "<root>\n<!-- 第一行\n第二行 -->\n</root>\n" {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("app 带属性", func(t *testing.T) {
		out, n, err := removeApps([]byte("<root>\n<app name=\"a\">b</app>\n</root>\n"), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != "<root>\n</root>\n" {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	// 处理是纯字节搬运，不该因为编码声明而失败
	t.Run("非 UTF-8 编码声明", func(t *testing.T) {
		in := `<?xml version="1.0" encoding="GBK"?><root><app>x</app></root>`
		out, n, err := removeApps([]byte(in), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if n != 1 || string(out) != `<?xml version="1.0" encoding="GBK"?><root></root>` {
			t.Errorf("n=%d out=%q", n, out)
		}
	})

	t.Run("UTF-8 BOM", func(t *testing.T) {
		in := "\xef\xbb\xbf<root>\n<app>x</app>\n</root>\n"
		out, _, err := removeApps([]byte(in), scopeAll, false)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if string(out) != "\xef\xbb\xbf<root>\n</root>\n" {
			t.Errorf("BOM 未保留: %q", out)
		}
	})
}

func TestParseScope(t *testing.T) {
	for in, want := range map[string]scope{"all": scopeAll, "method": scopeMethod, "ALL": scopeAll} {
		got, err := parseScope(in)
		if err != nil || got != want {
			t.Errorf("parseScope(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := parseScope("nope"); err == nil {
		t.Error("非法取值应当报错")
	}
}
