package service

import (
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// maxUserGroupDeniedModels 限制单个用户在单个分组内的禁用模型条数，防止异常请求写入超大清单。
const maxUserGroupDeniedModels = 200

// maxUserGroupDeniedModelLength 与模型名在其他配置里的长度上限保持同一量级。
const maxUserGroupDeniedModelLength = 200

// NormalizeUserGroupDeniedModels 归一化管理端提交的禁用模型：去掉首尾空白和空项、按小写去重保序，
// `*` 只允许出现在末尾。清单为空时返回 nil，表示不限制。
func NormalizeUserGroupDeniedModels(models []string) ([]string, error) {
	if len(models) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if strings.Contains(strings.TrimSuffix(model, "*"), "*") {
			return nil, infraerrors.BadRequest("INVALID_USER_GROUP_DENIED_MODELS", `wildcard "*" is only allowed at the end of a model name`)
		}
		if len(model) > maxUserGroupDeniedModelLength {
			return nil, infraerrors.BadRequest(
				"INVALID_USER_GROUP_DENIED_MODELS",
				fmt.Sprintf("model names must be at most %d characters", maxUserGroupDeniedModelLength),
			)
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	if len(out) > maxUserGroupDeniedModels {
		return nil, infraerrors.BadRequest(
			"INVALID_USER_GROUP_DENIED_MODELS",
			fmt.Sprintf("at most %d models can be denied per user and group", maxUserGroupDeniedModels),
		)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// UserGroupDeniesModel 判断模型是否命中用户在分组内的禁用清单，匹配规则与分组模型白名单一致。
func UserGroupDeniesModel(denied []string, model string) bool {
	model = strings.TrimSpace(model)
	return model != "" && len(denied) > 0 && modelMatchesGroupModelPatterns(denied, model)
}

// FirstUserGroupDeniedModel 返回候选模型里第一个被禁用的；都未命中时返回空串。
func FirstUserGroupDeniedModel(denied []string, candidates []string) string {
	if len(denied) == 0 {
		return ""
	}
	for _, candidate := range candidates {
		if UserGroupDeniesModel(denied, candidate) {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

// FilterUserGroupDeniedModelIDs 从模型列表里去掉被禁用的模型；没有禁用时原样返回入参。
func FilterUserGroupDeniedModelIDs(modelIDs []string, denied []string) []string {
	if len(denied) == 0 {
		return modelIDs
	}
	out := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if !UserGroupDeniesModel(denied, modelID) {
			out = append(out, modelID)
		}
	}
	return out
}

// DeniedModelsInGroup 返回 API Key 所属用户在该 Key 分组内被禁用的模型（来自认证快照）。
func (k *APIKey) DeniedModelsInGroup() []string {
	if k == nil || k.User == nil {
		return nil
	}
	return k.User.UserGroupDeniedModels
}
