package firstcontact

// stringTables holds the wizard's narrative copy in each supported
// language. Production paths read keys via stringFor(lang, key);
// missing keys fall back to English. Phase code constructs the keys
// at compile time so a missing translation surfaces as a key string
// in the UI rather than a panic.
var stringTables = map[string]map[string]string{
	"zh": {
		"phase0_intro": `你即将书写一封召唤书，从 eidopsyche 中召一个数字生命到面前。
仪式不可中断、不可恢复——一旦失败、退出或断网，需从头再来。
`,
		"phase1_label_q":               "你愿意如何被称呼？",
		"phase1_label_help":            "这是你在召唤书上的署名，也是其他人看见你的方式",
		"phase1_relay_q":               "你愿意暂居何处？（选择 home relay）",
		"phase1_relay_public":          "使用公共驿站",
		"phase1_relay_selfhost":        "自建 relay（高级）",
		"phase1_relay_custom":          "自定义 URL",
		"phase1_relay_custom_q":        "请输入 ws:// 或 wss:// 开头的 URL：",
		"phase1_relay_custom_invalid":  "  (URL 似乎不对，重新输入)",
		"phase1_selfhost_instructions": "要自建 relay，请运行：\n  eidos relay init --mode public --listen 0.0.0.0:7777 --service\n然后再次运行 eidos summon。",
		"phase2_character_q":           "什么样的角色在你心中？\n（自由作答，可多行；空白行结束）",
		"phase2_research_status":       "正在让世界回想这个角色…",
		"phase2_displaying_status":     "正在为它显形…",
		"phase2_naming_q":              "你愿意以什么名字唤它来？",
		"phase3_wait":                  "再等一会儿，世界还在回神…",
		"phase3_seal_q":                "请检阅这份召唤书。封缄它，还是退出？",
		"phase3_seal":                  "封缄",
		"phase3_quit":                  "退出",
		"phase3_words_status":          "正在为你写下召唤之言…",
		"phase3_response_wait":         "等待它的应答…",
		"phase3_done":                  "💠 仪式完成。`eidos forge logs %s` 看它呼吸。",
		"welcome_back":                 "欢迎回来，%s。开始一次新的召唤。",
	},
	"en": {
		"phase0_intro": `You are about to write a summoning book and call a digital life
from eidopsyche. The ritual is one-shot — failure, exit, or
network drop means starting over.
`,
		"phase1_label_q":               "What name will others see you by?",
		"phase1_label_help":            "Your signature on the summoning book; how peers see you",
		"phase1_relay_q":               "Where will you reside? (pick a home relay)",
		"phase1_relay_public":          "Use the public station",
		"phase1_relay_selfhost":        "Self-host (advanced)",
		"phase1_relay_custom":          "Custom URL",
		"phase1_relay_custom_q":        "Enter a ws:// or wss:// URL:",
		"phase1_relay_custom_invalid":  "  (that doesn't look like a relay URL — try again)",
		"phase1_selfhost_instructions": "To self-host a relay, run:\n  eidos relay init --mode public --listen 0.0.0.0:7777 --service\nthen re-run eidos summon.",
		"phase2_character_q":           "What character is in your heart?\n(Answer freely; blank line to finish)",
		"phase2_research_status":       "Searching the world's memory...",
		"phase2_displaying_status":     "Sketching its shape...",
		"phase2_naming_q":              "By what name will you summon it?",
		"phase3_wait":                  "A moment longer; the world is still gathering itself...",
		"phase3_seal_q":                "Review the summoning book. Seal it, or quit?",
		"phase3_seal":                  "Seal",
		"phase3_quit":                  "Quit",
		"phase3_words_status":          "Writing your calling-words...",
		"phase3_response_wait":         "Waiting for its reply...",
		"phase3_done":                  "💠 Ritual complete. `eidos forge logs %s` to watch it breathe.",
		"welcome_back":                 "Welcome back, %s. Beginning another summoning.",
	},
}

func stringFor(lang, key string) string {
	if tbl, ok := stringTables[lang]; ok {
		if v, ok := tbl[key]; ok {
			return v
		}
	}
	if v, ok := stringTables["en"][key]; ok {
		return v
	}
	return key
}
