package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	intelligentSVGElements   = strings.Fields("svg g defs title desc path rect circle ellipse line polyline polygon text tspan linearGradient radialGradient stop clipPath mask pattern")
	intelligentSVGAttributes = strings.Fields("id viewBox width height x y x1 x2 y1 y2 cx cy r rx ry d points fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-dasharray opacity transform font-size font-family font-weight text-anchor dominant-baseline offset stop-color stop-opacity gradientUnits gradientTransform spreadMethod clip-path clip-rule mask maskUnits patternUnits patternTransform preserveAspectRatio dx dy")
	svgCSSRule               = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	svgCSSSelector           = regexp.MustCompile(`^(?:[.#][A-Za-z_][A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9]*)$`)
	svgLocalURL              = regexp.MustCompile(`(?i)^url\(#[A-Za-z_][A-Za-z0-9_.-]*\)$`)
	svgDrawingPath           = regexp.MustCompile(`[LlHhVvCcSsQqTtAa]`)
)

type intelligentSVGNode struct {
	start    xml.StartElement
	children []*intelligentSVGNode
	text     string
}
type intelligentSVGRule struct {
	selector string
	values   map[string]string
}

func svgStaticValue(value string) bool {
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, "<>\x00\\") || strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") || strings.Contains(lower, "http:") || strings.Contains(lower, "https:") || strings.Contains(lower, "//") || strings.Contains(lower, "expression(") || strings.Contains(lower, "var(") {
		return false
	}
	return !strings.Contains(lower, "url(") || svgLocalURL.MatchString(strings.TrimSpace(value))
}

func svgPaintStyles(raw string) map[string]string {
	allowed := map[string]bool{}
	for _, key := range strings.Fields("fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-dasharray opacity font-size font-family font-weight text-anchor dominant-baseline stop-color stop-opacity") {
		allowed[key] = true
	}
	values := map[string]string{}
	for _, declaration := range strings.Split(raw, ";") {
		pair := strings.SplitN(declaration, ":", 2)
		if len(pair) != 2 {
			continue
		}
		key, value := strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1])
		if key == "display" && value == "none" {
			values["opacity"] = "0"
			continue
		}
		if allowed[key] && svgStaticValue(value) {
			values[key] = value
		}
	}
	return values
}

// PrepareIntelligentSVGPreview creates an inert SVG image. CSS is reduced to
// whitelisted static paint attributes; scripts, links, external resources and
// unsupported subtrees are never carried into the returned artifact.
func PrepareIntelligentSVGPreview(output string) (string, bool, error) {
	start, end := strings.Index(output, "<svg"), strings.LastIndex(output, "</svg>")
	if start < 0 || end < start {
		return "", false, errors.New("未返回完整 SVG 图像")
	}
	raw := output[start : end+len("</svg>")]
	if len(raw) > 128<<10 {
		return "", false, errors.New("SVG 超过 128 KiB 上限")
	}
	_, strictErr := SanitizeIntelligentTestSVG(raw)
	decoder := xml.NewDecoder(strings.NewReader(raw))
	var root *intelligentSVGNode
	var stack []*intelligentSVGNode
	nodes := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, errors.New("SVG XML 格式错误")
		}
		switch value := token.(type) {
		case xml.StartElement:
			nodes++
			if nodes > 6000 || len(stack) >= 40 {
				return "", false, errors.New("SVG 结构过于复杂")
			}
			node := &intelligentSVGNode{start: value}
			if len(stack) == 0 {
				if root != nil || value.Name.Local != "svg" {
					return "", false, errors.New("SVG 根节点错误")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return "", false, errors.New("SVG XML 格式错误")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				node := stack[len(stack)-1]
				node.children = append(node.children, &intelligentSVGNode{text: string(value)})
			}
		case xml.Comment:
		default:
			return "", false, errors.New("SVG 不允许指令或实体声明")
		}
	}
	if root == nil || len(stack) != 0 {
		return "", false, errors.New("SVG 不完整")
	}
	allowed, attrsAllowed := map[string]bool{}, map[string]bool{}
	for _, key := range intelligentSVGElements {
		allowed[key] = true
	}
	for _, key := range intelligentSVGAttributes {
		attrsAllowed[key] = true
	}
	var rules []intelligentSVGRule
	ids := map[string]*intelligentSVGNode{}
	var collect func(*intelligentSVGNode)
	collect = func(node *intelligentSVGNode) {
		name := node.start.Name.Local
		if node.start.Name.Space != "" && node.start.Name.Space != "http://www.w3.org/2000/svg" {
			return
		}
		if name == "style" {
			var css strings.Builder
			for _, child := range node.children {
				css.WriteString(child.text)
			}
			rawCSS := css.String()
			if len(rawCSS) >= 32<<10 || strings.Contains(rawCSS, "@") {
				return
			}
			for _, match := range svgCSSRule.FindAllStringSubmatch(rawCSS, 128) {
				for _, selector := range strings.Split(match[1], ",") {
					selector = strings.TrimSpace(selector)
					if svgCSSSelector.MatchString(selector) && len(rules) < 512 {
						rules = append(rules, intelligentSVGRule{selector, svgPaintStyles(match[2])})
					}
				}
			}
			return
		}
		if !allowed[name] && name != "use" {
			return
		}
		for _, attr := range node.start.Attr {
			if attr.Name.Space == "" && attr.Name.Local == "id" {
				ids[attr.Value] = node
			}
		}
		for _, child := range node.children {
			collect(child)
		}
	}
	collect(root)
	specificity := func(s string) int {
		if strings.HasPrefix(s, "#") {
			return 2
		}
		if strings.HasPrefix(s, ".") {
			return 1
		}
		return 0
	}
	sort.SliceStable(rules, func(i, j int) bool { return specificity(rules[i].selector) < specificity(rules[j].selector) })
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	drawable := 0
	rendered, depth := 0, 0
	active := map[*intelligentSVGNode]bool{}
	var render func(*intelligentSVGNode, bool) error
	render = func(node *intelligentSVGNode, visible bool) error {
		rendered++
		if rendered > 12000 || depth >= 50 || active[node] {
			return errors.New("SVG 引用循环或结构过于复杂")
		}
		depth++
		active[node] = true
		defer func() { depth--; delete(active, node) }()
		if node.start.Name.Local == "" {
			return encoder.EncodeToken(xml.CharData(node.text))
		}
		isUse := node.start.Name.Local == "use"
		if (!allowed[node.start.Name.Local] && !isUse) || (node.start.Name.Space != "" && node.start.Name.Space != "http://www.w3.org/2000/svg") {
			return nil
		}
		values := map[string]string{}
		id, classes, inline := "", "", ""
		for _, attr := range node.start.Attr {
			switch attr.Name.Local {
			case "id":
				id = attr.Value
			case "class":
				classes = " " + attr.Value + " "
			case "style":
				inline = attr.Value
			}
			if attr.Name.Space == "" && attrsAllowed[attr.Name.Local] && svgStaticValue(attr.Value) {
				values[attr.Name.Local] = attr.Value
			}
		}
		for _, rule := range rules {
			matches := rule.selector == node.start.Name.Local || (strings.HasPrefix(rule.selector, "#") && rule.selector[1:] == id) || (strings.HasPrefix(rule.selector, ".") && strings.Contains(classes, " "+rule.selector[1:]+" "))
			if matches {
				for key, value := range rule.values {
					values[key] = value
				}
			}
		}
		for key, value := range svgPaintStyles(inline) {
			values[key] = value
		}
		if opacity, err := strconv.ParseFloat(values["opacity"], 64); err == nil && opacity <= 0 {
			visible = false
		}
		switch node.start.Name.Local {
		case "defs", "clipPath", "mask", "pattern", "linearGradient", "radialGradient":
			visible = false
		}
		if visible && svgBasicDrawable(node.start.Name.Local, values) {
			drawable++
		}
		element := xml.StartElement{Name: xml.Name{Local: node.start.Name.Local}}
		var reference *intelligentSVGNode
		if isUse {
			for _, attr := range node.start.Attr {
				if attr.Name.Local == "href" && (attr.Name.Space == "" || attr.Name.Space == "http://www.w3.org/1999/xlink") && strings.HasPrefix(attr.Value, "#") {
					reference = ids[strings.TrimPrefix(attr.Value, "#")]
				}
			}
			if reference == nil {
				return nil
			}
			element.Name.Local = "g"
			x, y := values["x"], values["y"]
			if x == "" {
				x = "0"
			}
			if y == "" {
				y = "0"
			}
			if _, err := strconv.ParseFloat(x, 64); err != nil {
				return nil
			}
			if _, err := strconv.ParseFloat(y, 64); err != nil {
				return nil
			}
			values["transform"] = strings.TrimSpace(values["transform"] + " translate(" + x + " " + y + ")")
			delete(values, "x")
			delete(values, "y")
		}
		if node == root {
			element.Attr = append(element.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
		}
		for _, key := range intelligentSVGAttributes {
			if value, ok := values[key]; ok {
				element.Attr = append(element.Attr, xml.Attr{Name: xml.Name{Local: key}, Value: value})
			}
		}
		if err := encoder.EncodeToken(element); err != nil {
			return err
		}
		if reference != nil {
			if err := render(reference, visible); err != nil {
				return err
			}
		}
		for _, child := range node.children {
			if err := render(child, visible); err != nil {
				return err
			}
		}
		return encoder.EncodeToken(element.End())
	}
	if err := render(root, true); err != nil {
		return "", false, err
	}
	if err := encoder.Flush(); err != nil {
		return "", false, err
	}
	if out.Len() > 128<<10 {
		return "", false, errors.New("SVG 展开后超过 128 KiB 上限")
	}
	if drawable == 0 {
		return "", false, errors.New("SVG 未包含可显示的基本图形")
	}
	safe, err := SanitizeIntelligentTestSVG(out.String())
	return safe, strictErr != nil, err
}

func svgBasicDrawable(name string, values map[string]string) bool {
	positive := func(key string) bool {
		value, err := strconv.ParseFloat(strings.TrimSuffix(values[key], "%"), 64)
		return err == nil && value > 0
	}
	switch name {
	case "rect":
		return positive("width") && positive("height")
	case "circle":
		return positive("r")
	case "ellipse":
		return positive("rx") && positive("ry")
	case "path":
		return svgDrawingPath.MatchString(values["d"])
	case "line":
		return values["x1"] != values["x2"] || values["y1"] != values["y2"]
	case "polyline", "polygon":
		return len(strings.Fields(strings.ReplaceAll(values["points"], ",", " "))) >= 4
	}
	return false
}

func evaluateIntelligentSVG(output string) IntelligentTestEvaluation {
	detail := newIntelligentAssessment("svg_structure")
	detail["answer_verdict"] = "not_evaluated"
	detail["limitation"] = "图像已按安全 SVG 展示；鹈鹕、自行车和画面质量需要内容复核，结构检查不提供模型能力分数。"
	svg, adjusted, err := PrepareIntelligentSVGPreview(output)
	if err != nil {
		detail["reason"], detail["format_reason"], detail["image_state"] = err.Error(), err.Error(), "unavailable"
		return IntelligentTestEvaluation{Status: "completed", Detail: detail}
	}
	detail["image_state"], detail["format_verdict"], detail["reason"] = "ready", "compliant", "SVG 结构可显示，图像内容尚未评估"
	if adjusted {
		detail["image_state"], detail["format_verdict"], detail["format_reason"] = "sanitized", "non_compliant", "已将静态样式转换为安全属性，并移除不支持的内容；预览不执行原始 SVG 代码"
	}
	return IntelligentTestEvaluation{Status: "completed", Image: svg, Detail: detail}
}
