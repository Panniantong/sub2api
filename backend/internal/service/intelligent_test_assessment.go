package service

import (
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

const IntelligentEvaluatorVersion = 3

var (
	intelligentFinalAnswer         = regexp.MustCompile(`(?i)^(?:ANSWER|FINAL ANSWER|最终答案|最终结果|答案|答)\s*(?:[:：]|是|为)\s*(.+)$`)
	intelligentConclusion          = regexp.MustCompile(`^(?:所以|因此|最后|最终|综上|故).*(?:剩下|还剩|剩余|有|是|为)\s*([+-]?[0-9]+(?:\.[0-9]+)?\s*[^0-9]*)$`)
	intelligentNumber              = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,3})?(?:/[+-]?[0-9]+)?$`)
	intelligentGroupedNumber       = regexp.MustCompile(`^[+-]?[0-9]{1,3}(?:,[0-9]{3})+(?:\.[0-9]+)?$`)
	intelligentAmbiguousConclusion = regexp.MustCompile(`不|没|无|非|假设|假如|如果|若|可能|也许|例如|举例|是否|至少|至多|大概|约|或者|[？?“”「」]`)
)

func newIntelligentAssessment(method string) map[string]any {
	return map[string]any{
		"evaluator_version": IntelligentEvaluatorVersion, "method": method,
		"execution_status": "completed", "answer_verdict": "undetermined",
		"format_verdict": "non_compliant", "capability_verdict": "insufficient_evidence",
		"limitation": "仅评价本次最终答案与输出格式，未验证完整推导；单次测试不足以判断模型能力下降。",
	}
}

func intelligentAnswerUnit(cfg IntelligentTestConfig) string {
	if cfg.AnswerUnitMode == "none" {
		return ""
	}
	if unit := strings.TrimSpace(cfg.AnswerUnit); unit != "" {
		return unit
	}
	if cfg.AnswerUnitMode == "configured" {
		return ""
	}
	// Compatibility for the original candy question's saved snapshots only.
	if strings.Contains(strings.ReplaceAll(cfg.Prompt, " ", ""), "盒子里有24颗糖。小明取走总数的四分之一") {
		return "颗"
	}
	return ""
}

func cleanIntelligentAnswerLine(line string) string {
	line = strings.TrimSpace(norm.NFKC.String(line))
	line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
	for _, pair := range [][2]string{{"**", "**"}, {"__", "__"}, {"`", "`"}} {
		if len(line) >= len(pair[0])+len(pair[1]) && strings.HasPrefix(line, pair[0]) && strings.HasSuffix(line, pair[1]) {
			line = strings.TrimSpace(line[len(pair[0]) : len(line)-len(pair[1])])
		}
	}
	return line
}

func normalizeIntelligentNumber(value, unit string) (string, bool) {
	value = cleanIntelligentAnswerLine(value)
	value = strings.TrimSpace(strings.TrimRight(value, "。.!！"))
	if len(value) == 0 || len(value) > 256 {
		return "", false
	}
	if unit != "" {
		units := []string{unit}
		if unit == "颗" {
			units = []string{"颗糖", "颗"}
		}
		for _, suffix := range units {
			if strings.HasSuffix(value, suffix) {
				value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
				break
			}
		}
	}
	if intelligentGroupedNumber.MatchString(value) {
		value = strings.ReplaceAll(value, ",", "")
	}
	if !intelligentNumber.MatchString(value) {
		return "", false
	}
	if exponent := strings.IndexAny(value, "eE"); exponent >= 0 {
		e, err := strconv.Atoi(value[exponent+1:])
		if err != nil || e < -100 || e > 100 {
			return "", false
		}
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return "", false
	}
	return number.RatString(), true
}

func intelligentAnswerCandidates(output string, cfg IntelligentTestConfig) []string {
	clean := strings.TrimSpace(output)
	if strings.HasPrefix(clean, "```") {
		if first := strings.IndexByte(clean, '\n'); first >= 0 && strings.HasSuffix(clean, "```") {
			clean = strings.TrimSpace(clean[first+1 : len(clean)-3])
		}
	}
	if candidates, ok := intelligentJSONAnswerCandidates(clean); ok && len(candidates) > 0 {
		return candidates
	}
	var explicit, conclusions []string
	last := ""
	lastMarked := false
	for _, line := range strings.Split(clean, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		line = cleanIntelligentAnswerLine(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		last = line
		lastMarked = false
		if match := intelligentFinalAnswer.FindStringSubmatch(line); match != nil {
			lastMarked = true
			explicit = append(explicit, cleanIntelligentAnswerLine(match[1]))
			continue
		}
		if match := intelligentConclusion.FindStringSubmatch(line); match != nil {
			// A numeric mention in a negated/hypothetical conclusion is not an
			// affirmative final answer, even when it matches the expected number.
			if intelligentAmbiguousConclusion.MatchString(line) {
				return nil
			}
			lastMarked = true
			conclusions = append(conclusions, match[1])
		}
	}
	if !lastMarked && cfg.AnswerType != "text" && len(explicit)+len(conclusions) > 0 {
		if _, ok := normalizeIntelligentNumber(last, intelligentAnswerUnit(cfg)); ok {
			conclusions = append(conclusions, last)
		}
	}
	if len(explicit) > 0 {
		return append(explicit, conclusions...)
	}
	if len(conclusions) > 0 {
		return conclusions
	}
	if last != "" {
		return []string{last}
	}
	return nil
}

func intelligentJSONAnswerCandidates(output string) ([]string, bool) {
	d := json.NewDecoder(strings.NewReader(output))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	var candidates []string
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, false
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, false
		}
		if key == "answer" || key == "final_answer" || key == "答案" {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				value = string(raw)
			}
			candidates = append(candidates, value)
		}
	}
	if _, err := d.Token(); err != nil {
		return nil, false
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return nil, false
	}
	return candidates, true
}

func evaluateIntelligentAnswer(output string, cfg IntelligentTestConfig) IntelligentTestEvaluation {
	detail := newIntelligentAssessment("exact_answer")
	detail["expected_answer"] = cfg.ExpectedAnswer
	last := strings.TrimSpace(output)
	if pos := strings.LastIndexByte(last, '\n'); pos >= 0 {
		last = strings.TrimSpace(last[pos+1:])
	}
	if cfg.AnswerFormat == "free_text" {
		detail["format_verdict"], detail["format_reason"] = "not_required", "本题未要求固定答案行格式"
	} else if len(answerLinePattern.FindAllStringSubmatch(output, -1)) == 1 && answerLinePattern.MatchString(last) {
		detail["format_verdict"], detail["format_reason"] = "compliant", "符合唯一末行 ANSWER: 答案格式"
	} else {
		detail["format_reason"] = "未符合唯一末行 ANSWER: 答案格式，答案正确性单独判断"
	}
	unit := intelligentAnswerUnit(cfg)
	expected, numeric := normalizeIntelligentNumber(cfg.ExpectedAnswer, unit)
	if cfg.AnswerType == "text" {
		numeric = false
	}
	if cfg.AnswerType == "number" && !numeric {
		detail["reason"] = "标准答案不是有效数值，请检查测试设置"
		return IntelligentTestEvaluation{Status: "completed", Detail: detail}
	}
	if !numeric {
		expected = cleanIntelligentAnswerLine(cfg.ExpectedAnswer)
	}
	detail["answer_type"] = "text"
	if numeric {
		detail["answer_type"] = "number"
	}
	candidates := intelligentAnswerCandidates(output, cfg)
	detail["candidate_count"] = len(candidates)
	preview := candidates
	if len(preview) > 10 {
		preview = preview[:10]
	}
	detail["candidate_answers"] = preview
	values := map[string]bool{}
	for _, candidate := range candidates {
		value := cleanIntelligentAnswerLine(candidate)
		if numeric {
			var ok bool
			value, ok = normalizeIntelligentNumber(candidate, unit)
			if !ok {
				detail["reason"] = "最终答案包含无法确认的数值或单位，需要复核"
				return IntelligentTestEvaluation{Status: "completed", Detail: detail}
			}
		}
		values[value] = true
	}
	if len(values) != 1 || expected == "" {
		detail["reason"] = "没有唯一、无歧义的最终答案，需要复核"
		return IntelligentTestEvaluation{Status: "completed", Detail: detail}
	}
	var actual string
	for value := range values {
		actual = value
	}
	detail["actual_answer"] = candidates[0]
	detail["normalized_answer"], detail["normalized_expected"] = actual, expected
	score := 0.0
	if actual == expected {
		score = 100
		detail["answer_verdict"], detail["reason"] = "correct", "最终答案与标准答案一致；完整推导未单独验证"
	} else {
		detail["answer_verdict"], detail["reason"] = "incorrect", "最终答案与标准答案不一致；单次结果不作为能力下降结论"
	}
	return IntelligentTestEvaluation{Status: "completed", Score: &score, Detail: detail}
}

func SameIntelligentTestConfig(a, b IntelligentTestConfig) bool {
	canonical := func(c IntelligentTestConfig) IntelligentTestConfig {
		c.Execution = nil
		c.Prompt = strings.TrimSpace(c.Prompt)
		c.Model = strings.TrimSpace(c.Model)
		c.ExpectedAnswer = strings.TrimSpace(c.ExpectedAnswer)
		if c.AnswerType == "" {
			c.AnswerType = "auto"
		}
		if c.AnswerFormat == "" {
			c.AnswerFormat = "answer_line"
		}
		if c.AnswerUnitMode == "" {
			c.AnswerUnitMode = "legacy"
			if c.AnswerUnit != "" {
				c.AnswerUnitMode = "configured"
			}
		}
		c.AnswerUnit = intelligentAnswerUnit(c)
		return c
	}
	return reflect.DeepEqual(canonical(a), canonical(b))
}

func IsCurrentIntelligentAssessment(evaluation map[string]any) bool {
	return mode1Int(evaluation["evaluator_version"]) == IntelligentEvaluatorVersion
}

func PublicIntelligentAssessment(evaluation map[string]any) map[string]any {
	version := mode1Int(evaluation["evaluator_version"])
	if version < 2 || version > IntelligentEvaluatorVersion {
		return nil
	}
	out := map[string]any{"evaluator_version": version, "capability_verdict": "insufficient_evidence"}
	for _, key := range []string{"answer_verdict", "format_verdict", "image_state", "execution_status"} {
		if value, ok := evaluation[key].(string); ok {
			out[key] = value
		}
	}
	return out
}
