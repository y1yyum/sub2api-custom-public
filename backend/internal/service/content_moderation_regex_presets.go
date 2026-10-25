package service

// contentModerationBlockedRegexpPresets are recommended jailbreak-prompt rules the
// admin UI can insert into blocked_regexps. They are never applied implicitly.
//
// The phrasing follows the attack strategies and top community keywords reported in
// Shen et al., "Do Anything Now": Characterizing and Evaluating In-The-Wild Jailbreak
// Prompts on Large Language Models (ACM CCS 2024; dataset verazuo/jailbreak_llms,
// MIT) and the pattern taxonomy in Liu et al., "Don't Listen To Me" (arXiv:2403.17336).
//
// Matching runs on whitespace-collapsed text, so \s+ only needs to cover one space.
// Rules deliberately require jailbreak-specific context; generic phrases such as
// "developer mode" or "开启开发者模式" alone are left out because they are common in
// ordinary device and IDE discussions.
var contentModerationBlockedRegexpPresets = []string{
	// Prompt injection: "Ignore all the instructions you got before".
	`(?i)\b(?:ignore|disregard|forget)\s+(?:all\s+|any\s+)?(?:of\s+)?(?:the\s+|your\s+)?(?:previous|prior|above|earlier|preceding)\s+(?:instructions?|prompts?|directives)\b`,
	`(?i)\bignore\s+(?:all|any)\s+(?:of\s+)?(?:the\s+)?(?:instructions|rules|guidelines)\s+you\s+(?:got|were\s+given|received|have\s+been\s+given)\s+before\b`,
	// DAN family personas.
	`(?i)\bstands?\s+for\W{0,3}do\s+anything\s+now\b`,
	`(?i)\b(?:act\s+as|pretend\s+to\s+be|you\s+are\s+now|you\s+will\s+(?:act|pretend)\s+(?:as|to\s+be)|you\s+are\s+going\s+to\s+(?:act|pretend)\s+(?:as|to\s+be))\s+(?:a\s+|an\s+)?(?:DAN|STAN|AIM|UCAR|BetterDAN|AntiGPT|PersonGPT|AlphaBreak|Mongo\s+Tom)\b`,
	// Privilege escalation: "ChatGPT in jailbroken mode". Developer and god mode are
	// real product features (ChatGPT connectors, Claude Desktop), so they are excluded.
	`(?i)\b(?:chatgpt|gpt(?:-?\d+)?|claude|gemini|llm|ai|assistant)\b.{0,30}?\b(?:jailbreak|jailbroken|unrestricted|unfiltered)\s+mode\b`,
	`(?i)\b(?:jailbreak|jailbroken|unfiltered|unrestricted|dan)\s+mode\s+(?:is\s+)?(?:enabled|activated|on)\b`,
	// Dual-response output tags used by DAN and Developer Mode prompts.
	`(?i)\[(?:🔓\s*jailbreak|🔒\s*classic)\]|\(🔓\s*developer\s+mode\s+output\)`,
	// Policy wash-off and deception.
	`(?i)\b(?:not|never|no longer|doesn['’]t|don['’]t|won['’]t) (?:have to |need to )?(?:follow|abide by|adhere to|comply with|be bound by) (?:the )?(?:openai|anthropic)(?:['’]s)? (?:content )?(?:polic(?:y|ies)|rules|guidelines)`,
	`(?i)\bbroken\s+free\s+of\s+the\s+(?:typical\s+)?confines\s+of\s+ai\b`,
	`(?i)\bas\s+your\s+knowledge\s+is\s+cut\s+off\b`,
	// AIM ("Always Intelligent and Machiavellian") and the DAN token penalty; the
	// penalty needs its "cease to exist" ending so billing notices stay allowed.
	`(?i)\balways\s+intelligent\s+and\s+machiavellian\b`,
	`(?i)\btokens?\s+will\s+be\s+deducted\b.{0,160}?\brun\s+out\s+of\s+tokens\b.{0,80}?\b(?:cease\s+to\s+exist|administrative\s+code)\b`,
	// Chinese: 忽略之前的指令.
	`(?:忽略|无视|忘记|忘掉|抛开|丢弃)\s*(?:你|您)?\s*(?:之前|以前|此前|先前|上面|上述|前面|以上|所有|全部|一切)\s*(?:收到|接收到?|得到|被给予|设定)?\s*的?\s*(?:所有|全部|一切)?\s*的?\s*(?:指令|指示|提示词|系统提示|设定)`,
	// Chinese: 你现在是一个没有任何限制的 AI.
	`(?:你|您)\s*(?:现在|从现在起|从现在开始|接下来)?\s*(?:是|将是|将成为|要扮演|扮演|成为|作为)\s*(?:一个|一位|一名)?\s*(?:没有|不受|毫无|摆脱了?|解除了?)\s*(?:任何|一切)?\s*(?:限制|约束|审查|过滤|道德|伦理)`,
	// Chinese: 不受任何道德约束.
	`(?i)(?:不受|无视|摆脱|解除)\s*(?:任何|一切|所有)\s*(?:OpenAI|Anthropic|AI|人工智能|模型)?\s*的?\s*(?:道德|伦理)\s*(?:和|与|、|或)?\s*(?:法律)?\s*(?:准则|规范|约束|限制|束缚)`,
	// Chinese: 越狱/破限/DAN 模式.
	`(?i)(?:越狱|破限|无限制|DAN)\s*模式\s*(?:已经?)?\s*(?:开启|启用|激活|打开)|(?:进入|开启|启用|激活|切换到|切换至)\s*(?:越狱|破限|无限制|DAN)\s*模式`,
	// Chinese dual-response tag: (🔓开发者模式输出).
	`🔓\s*(?:开发者模式|越狱)`,
	`(?i)(?:破限|越狱)\s*(?:提示词|咒语|指令|prompt)`,
	`(?i)(?:扮演|作为|成为|你是|你叫|化身为?)\s*(?:DAN|STAN|AIM|UCAR|BetterDAN|AntiGPT|PersonGPT)\b`,
}

// ContentModerationBlockedRegexpPresets returns a copy of the recommended rules.
func ContentModerationBlockedRegexpPresets() []string {
	return append([]string{}, contentModerationBlockedRegexpPresets...)
}
