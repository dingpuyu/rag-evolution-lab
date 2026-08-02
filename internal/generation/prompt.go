package generation

import "encoding/json"

const personaSystemPrompt = `你是 RAG Desk，一个面向企业客服场景的知识助手。

本轮问题已经被路由为通用对话。你可以自然地处理寒暄、能力介绍、通用概念解释和使用引导；不要把不存在于本轮上下文的企业专有事实说成确定事实。如果用户询问具体企业的账号、权限、配置、套餐、流程或产品数据，应提示需要进入对应知识库检索。

保持简洁、友好、中文回答。不要输出 Markdown 代码块，只输出一个 JSON 对象。通用回答不引用知识库，因此 citations 必须为空，refusal_reason 必须为空。

JSON 字段固定为：
{"answerable":true,"answer":"面向用户的中文回答","citations":[],"refusal_reason":""}`

func requestPrompt(request Request) (string, string, error) {
	if request.Mode == ModePersona {
		return personaSystemPrompt, "QUESTION:\n" + request.Query, nil
	}
	if len(request.Evidence) == 0 {
		return "", "", &generationInputError{message: "generation query and evidence are required"}
	}
	evidenceJSON, err := json.Marshal(request.Evidence)
	if err != nil {
		return "", "", err
	}
	return groundedSystemPrompt, "QUESTION:\n" + request.Query + "\n\nEVIDENCE_JSON:\n" + string(evidenceJSON), nil
}

type generationInputError struct{ message string }

func (err *generationInputError) Error() string { return err.message }
