package generation

import "strings"

// IsGeneralQuery is a deliberately conservative, deterministic first-pass
// router. It keeps greetings, capability questions and generic concepts out of
// the enterprise corpus while sending product, account and policy questions
// through RAG. It is not an authorization decision; domain queries still rely
// on the server-side dataset filter and grounded answer contract.
func IsGeneralQuery(query string) bool {
	value := strings.ToLower(strings.TrimSpace(query))
	if value == "" {
		return false
	}
	for _, keyword := range []string{
		"企业", "产品", "账号", "账户", "权限", "登录", "单点", "sso", "saml", "oidc",
		"报表", "导出", "服务", "故障", "租户", "套餐", "配置", "api", "接口", "知识库",
		"文档", "系统", "项目", "合同", "订单", "价格", "配额", "额度", "回滚", "索引",
		"milvus", "embedding", "向量", "ragflow", "客服工作台", "工作流",
	} {
		if strings.Contains(value, keyword) {
			return false
		}
	}
	for _, phrase := range []string{
		"你好", "您好", "嗨", "哈喽", "在吗", "谢谢", "感谢", "再见",
		"你是谁", "你能做什么", "你的能力", "怎么使用你", "如何使用你",
		"介绍一下自己", "介绍一下你", "什么是rag", "请问", "可以吗", "能不能",
		"帮我", "请帮我", "what are you", "who are you", "what can you do", "hello",
	} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	for _, phrase := range []string{"什么是", "请解释", "解释一下", "为什么", "如何", "怎么", "能否", "给我", "写一个", "总结一下", "翻译", "why ", "how does", "what is"} {
		if strings.HasPrefix(value, phrase) || strings.Contains(value, phrase) {
			return true
		}
	}
	// Unknown questions stay on the grounded path. This conservative default
	// prevents an unrecognised enterprise fact from being answered by model
	// priors just because it is short.
	return false
}
