// Command brightness-xml 删除 ColorOS 亮度配置文件里的 <app> 元素。
//
// /my_product/vendor/etc/display_brightness_app_list.xml 列出了系统会主动下调
// 屏幕亮度的应用。把 <app> 元素整个删除后，系统匹配不到任何应用，屏幕亮度
// 就不会再被下调。
//
// 必须是**删除元素**，而不是把元素内容掏空：留下 <app></app> 这样的空元素，
// 系统会把空字符串当包名去解析，导致无法开机。
//
// 除 <app> 元素本身（及其紧邻的说明注释）外，输入文件的字节全部原样保留：
// XML 声明、其余注释、缩进、version、各 <method> 下的 nit/ratio 等参数都不变。
// 模块在刷入时读设备上的原文件来生成配置，因此机型相关的参数仍是设备自己的。
package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const progName = "brightness-xml"

// appTag 是目标元素名。
const appTag = "app"

// scope 指定哪些 <app> 需要被删除。
type scope int

const (
	scopeAll    scope = iota // 全部 <app>
	scopeMethod              // 只处理 <method> 内的 <app>
)

func (s scope) matches(inMethod bool) bool {
	switch s {
	case scopeAll:
		return true
	case scopeMethod:
		return inMethod
	default:
		return false
	}
}

func parseScope(v string) (scope, error) {
	switch strings.ToLower(v) {
	case "all":
		return scopeAll, nil
	case "method":
		return scopeMethod, nil
	default:
		return 0, fmt.Errorf("无效的 -scope 取值 %q，可选 all 或 method", v)
	}
}

// span 是输入中的半开字节区间 [start, end)。
type span struct{ start, end int }

func main() {
	outPath := flag.String("o", "-", "输出路径，- 表示写到标准输出")
	scopeName := flag.String("scope", "all", "处理范围：all=全部 <app>，method=仅 <method> 内的 <app>")
	keepComments := flag.Bool("keep-comments", false, "保留 <app> 上方紧邻的说明注释（默认随 <app> 一并删除）")
	quiet := flag.Bool("q", false, "不输出统计信息")
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, "用法: %s [选项] <输入文件|->\n\n", progName)
		fmt.Fprintln(w, "删除 display_brightness_app_list.xml 中的 <app> 元素，其余内容原样保留。")
		fmt.Fprintln(w)
		flag.PrintDefaults()
	}
	flag.Parse()

	sc, err := parseScope(*scopeName)
	if err != nil {
		fatal(err)
	}
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	input := flag.Arg(0)
	data, err := readInput(input)
	if err != nil {
		fatal(err)
	}

	result, n, err := removeApps(data, sc, *keepComments)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", input, err))
	}
	if n == 0 {
		// 一个都没删说明文件结构不符合预期，此时写出结果只会白白覆盖系统配置
		fatal(fmt.Errorf("%s: 未找到可删除的 <app> 元素", input))
	}

	// 防御性自检：<app> 必须被彻底删除。若留下 <app></app> 这类空元素，系统会
	// 把空字符串当包名解析并导致无法开机 —— 这是本工具最不能出的错，宁可失败
	// 也不写出这样的文件。
	if left, err := findAppSpans(result, sc); err != nil {
		fatal(fmt.Errorf("自检失败: %w", err))
	} else if len(left) > 0 {
		fatal(fmt.Errorf("自检失败: 输出中仍残留 %d 个 <app> 元素，已放弃写出", len(left)))
	}

	if err := writeOutput(*outPath, result); err != nil {
		fatal(err)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "%s: 删除 %d 个 <app>，输出 %d 字节\n", progName, n, len(result))
	}
}

// removeApps 删除 <app> 元素，返回结果与被删除的元素个数。
//
// 实现上刻意避开 Unmarshal/Marshal —— 那会丢掉注释、缩进和元素顺序。这里逐
// token 扫描，用 InputOffset 记录每个元素在输入中的字节区间，再把这些区间从
// 输入里剔除，其余字节原样搬运。因此 <app> 之外的内容逐字节不变。
func removeApps(data []byte, sc scope, keepComments bool) ([]byte, int, error) {
	spans, err := findAppSpans(data, sc)
	if err != nil {
		return nil, 0, err
	}
	if len(spans) == 0 {
		return data, 0, nil
	}
	n := len(spans)

	// 先把元素扩展为它独占的整行，再按需吃掉紧邻其上的说明注释，
	// 扩展后相邻区间可能相接，需要合并（过滤时区间必须有序且不重叠）。
	for i := range spans {
		spans[i] = expandToLine(data, spans[i])
	}
	if !keepComments {
		for i := range spans {
			spans[i] = expandOverLeadingComments(data, spans[i])
		}
	}
	spans = mergeSpans(spans)

	return filterSpans(data, spans), n, nil
}

// findAppSpans 扫出所有需要删除的 <app> 元素的字节区间。
func findAppSpans(data []byte, sc scope) ([]span, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// 设备上的配置未必严格规范（未定义实体、裸 & 等），放宽校验，
	// 免得无关紧要的瑕疵让整个文件处理失败。
	dec.Strict = false
	// 只做字节搬运，不解码文本，所以声明的编码是 GBK 还是别的都无所谓 ——
	// 直接按原始字节读入，避免因编码不支持而整份文件处理失败。
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }

	var (
		spans    []span
		prev     int
		start    = -1 // 当前 <app> 的起始偏移，-1 表示不在 <app> 内
		depth    int  // <app> 内的嵌套层数
		inMethod int
	)

	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解析 XML 失败: %w", err)
		}

		cur := int(dec.InputOffset())
		rawStart := prev
		prev = cur

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "method" {
				inMethod++
			}
			if t.Name.Local == appTag {
				if start < 0 {
					if sc.matches(inMethod > 0) {
						start = rawStart
						depth = 0
					}
				} else {
					depth++ // <app> 内嵌套同名标签
				}
			}

		case xml.EndElement:
			if t.Name.Local == appTag && start >= 0 {
				if depth > 0 {
					depth--
				} else {
					spans = append(spans, span{start, cur})
					start = -1
				}
			}
			if t.Name.Local == "method" {
				inMethod--
			}
		}
	}

	if start >= 0 {
		// 放着不管会把后半段文件整个丢掉，宁可失败也不写出残文件
		return nil, errors.New("XML 结构异常：<app> 没有正常闭合")
	}
	return spans, nil
}

// expandToLine 把元素区间扩展为它在输入中独占的整行（含行首缩进与行尾换行）。
// 仅当元素前后除空白外没有别的内容时才扩展，否则保持原区间 —— 避免误删同一行
// 上的其他内容（如 <method id="0"><app>x</app></method> 里的 method 标签）。
func expandToLine(data []byte, s span) span {
	// 行首：往前回溯到上一个换行符之后
	if j := bytes.LastIndexByte(data[:s.start], '\n') + 1; isBlank(data[j:s.start]) {
		s.start = j
	}
	// 行尾：往后找到换行符，连同它一起删掉
	if j := bytes.IndexByte(data[s.end:], '\n'); j >= 0 {
		if isBlank(data[s.end : s.end+j]) {
			s.end += j + 1
		}
	} else if isBlank(data[s.end:]) {
		s.end = len(data) // 末行没有换行符
	}
	return s
}

// expandOverLeadingComments 把区间向上扩展，吃掉紧邻其上的单行说明注释。
// <app> 前的注释是给该应用做说明的（如 "<!-- 浏览器 -->"），元素删除后这些
// 注释不再有意义，一并删除更干净。只认单行注释：跨行注释无法靠一次回溯安全
// 识别，遇到就停下。
func expandOverLeadingComments(data []byte, s span) span {
	for s.start > 0 && data[s.start-1] == '\n' {
		lineStart := bytes.LastIndexByte(data[:s.start-1], '\n') + 1
		line := bytes.TrimSpace(data[lineStart : s.start-1])
		if !bytes.HasPrefix(line, []byte("<!--")) || !bytes.HasSuffix(line, []byte("-->")) {
			break
		}
		s.start = lineStart
	}
	return s
}

// mergeSpans 合并重叠或相接的区间。合并后区间有序且互不重叠。
func mergeSpans(spans []span) []span {
	if len(spans) < 2 {
		return spans
	}
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// filterSpans 剔除给定区间，返回其余字节。区间须有序且不重叠。
func filterSpans(data []byte, spans []span) []byte {
	out := make([]byte, 0, len(data))
	prev := 0
	for _, s := range spans {
		out = append(out, data[prev:s.start]...)
		prev = s.end
	}
	return append(out, data[prev:]...)
}

// isBlank 判断是否全为空白（含行内与换行空白）。
func isBlank(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
		default:
			return false
		}
	}
	return true
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// writeOutput 写出结果。目标位置即使已有同名文件也不会留下半个文件：先写同
// 目录下的临时文件再重命名，中途失败时原文件保持不变。
func writeOutput(path string, data []byte) error {
	if path == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}

	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("无法在 %s 创建临时文件: %w", dir, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // 重命名成功后这里是无操作

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", progName, err)
	os.Exit(1)
}
