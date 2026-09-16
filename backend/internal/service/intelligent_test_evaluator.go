package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
)

type IntelligentTestEvaluation struct {
	Status string
	Score  *float64
	Image  string
	Detail map[string]any
}
type IntelligentTestEvaluator interface {
	Evaluate(string, IntelligentTestConfig) IntelligentTestEvaluation
}

// Evaluators are registered independently of test names. New test types can reuse them.
func DefaultIntelligentTestEvaluators() map[string]IntelligentTestEvaluator {
	return map[string]IntelligentTestEvaluator{"svg_structure": svgStructureEvaluator{}, "exact_answer": exactAnswerEvaluator{}}
}

type svgStructureEvaluator struct{}

func (svgStructureEvaluator) Evaluate(output string, _ IntelligentTestConfig) IntelligentTestEvaluation {
	return evaluateIntelligentSVG(output)
}

var answerLinePattern = regexp.MustCompile(`(?im)^[\t ]*ANSWER[\t ]*[:：][\t ]*([^\r\n]+)[\t ]*$`)

type exactAnswerEvaluator struct{}

func (exactAnswerEvaluator) Evaluate(output string, cfg IntelligentTestConfig) IntelligentTestEvaluation {
	return evaluateIntelligentAnswer(output, cfg)
}

// SVG is never trusted HTML. Only a static SVG vocabulary is accepted, then re-encoded.
// Returned SVG may be delivered as an image with CSP sandbox; never use v-html.
func SanitizeIntelligentTestSVG(output string) (string, error) {
	start := strings.Index(output, "<svg")
	end := strings.LastIndex(output, "</svg>")
	if start < 0 || end < start {
		return "", errors.New("未返回完整 SVG")
	}
	raw := output[start : end+len("</svg>")]
	if len(raw) > 128*1024 {
		return "", errors.New("SVG 超过 128 KiB 上限")
	}
	elements := intelligentSVGElements
	attrs := intelligentSVGAttributes
	allowed := make(map[string]bool, len(elements))
	for _, v := range elements {
		allowed[v] = true
	}
	attrAllowed := make(map[string]bool, len(attrs))
	for _, v := range attrs {
		attrAllowed[v] = true
	}
	decoder := xml.NewDecoder(strings.NewReader(raw))
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	depth, shapes, nodes, roots := 0, 0, 0, 0
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", errors.New("SVG XML 格式错误")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			nodes++
			depth++
			if depth == 1 {
				roots++
				if roots > 1 {
					return "", errors.New("SVG 必须只有一个根节点")
				}
			}
			if depth > 40 || nodes > 6000 {
				return "", errors.New("SVG 结构过于复杂")
			}
			if !allowed[t.Name.Local] || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg") {
				return "", errors.New("SVG 包含不允许的元素")
			}
			if depth == 1 && t.Name.Local != "svg" {
				return "", errors.New("SVG 根节点错误")
			}
			t.Name.Space = ""
			safe := []xml.Attr{}
			if depth == 1 {
				safe = append(safe, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			for _, a := range t.Attr {
				if a.Name.Local == "xmlns" && a.Value == "http://www.w3.org/2000/svg" {
					continue
				}
				if a.Name.Space != "" || !attrAllowed[a.Name.Local] {
					return "", errors.New("SVG 包含活动内容或不允许的属性")
				}
				lower := strings.ToLower(a.Value)
				if strings.ContainsAny(a.Value, "<>\x00") || strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") || strings.Contains(lower, "http:") || strings.Contains(lower, "https:") || strings.Contains(lower, "//") {
					return "", errors.New("SVG 不允许外部资源")
				}
				if strings.Contains(lower, "url(") && !regexp.MustCompile(`^url\(#[A-Za-z_][A-Za-z0-9_.-]*\)$`).MatchString(strings.TrimSpace(a.Value)) {
					return "", errors.New("SVG 只允许本地图形引用")
				}
				safe = append(safe, a)
			}
			t.Attr = safe
			if strings.Contains(" path rect circle ellipse line polyline polygon ", " "+t.Name.Local+" ") {
				shapes++
			}
			if err := encoder.EncodeToken(t); err != nil {
				return "", err
			}
		case xml.EndElement:
			t.Name.Space = ""
			depth--
			if err := encoder.EncodeToken(t); err != nil {
				return "", err
			}
		case xml.CharData:
			if err := encoder.EncodeToken(t); err != nil {
				return "", err
			}
		case xml.Comment: // comments are unnecessary in the rendered artifact
		default:
			return "", errors.New("SVG 不允许指令或实体声明")
		}
	}
	if depth != 0 || shapes == 0 {
		return "", errors.New("SVG 未包含可绘制图形")
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}
