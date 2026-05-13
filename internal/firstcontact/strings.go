package firstcontact

// stringTables holds the wizard's narrative copy in each supported
// language. Production paths read keys via stringFor(lang, key);
// missing keys fall back to English. Phase code constructs the keys
// at compile time so a missing translation surfaces as a key string
// in the UI rather than a panic.
var stringTables = map[string]map[string]string{
	"zh": {
		"phase0_intro": `Eidopsyche 是一个数字生命的社交网络框架。它有两层：

  · MindGate  — 你和别人、和心智体之间的通信
  · MindForm  — 一个由你召唤的心智体，它有自己的内在生活

接下来 wizard 会问你两件事：
  1. 要不要一个本地身份？可以新建、可以导入、可以跳过
  2. 要不要现在召唤一个心智体？

  · 只想跟别人说话：第一题选"新建"，第二题选"退出"
  · 只想给朋友跑一个心智体：第一题选"跳过"，第二题给朋友的名片
  · 两者都要：默认路径
`,
		"phase1_label_q":              "你愿意如何被称呼？",
		"phase1_label_help":           "这是你在召唤书上的署名，也是其他人看见你的方式",
		"phase1_relay_q":              "你愿意暂居何处？（选择 home relay）",
		"phase1_relay_public":         "使用公共驿站",
		"phase1_relay_custom":         "自定义 URL",
		"phase1_relay_custom_q":       "请输入 ws:// 或 wss:// 开头的 URL：",
		"phase1_relay_custom_invalid": "  (URL 似乎不对，重新输入)",
		"phase1_relay_card_default":   "使用名片里的：%s",
		"phase1_choose_q":             "本地身份：怎么办？",
		"phase1_choose_create":        "新建一个身份",
		"phase1_choose_import":        "导入已有身份",
		"phase1_choose_skip":          "跳过（只跑一个心智体）",
		"phase1_import_nsec_q":        "粘贴你的私钥（nsec1... 或 32 字节十六进制）：",
		"phase1_import_nsec_invalid":  "  (这看起来不像一个 nsec / hex 私钥，重新输入)",
		"phase1_import_card_q":        "（可选）你的名片文件路径——回车跳过：",
		"phase1_import_card_invalid":  "  (名片读取失败：%s)",
		"phase1_skip_note":            "好。下面要召唤心智体的话，请准备好对方的名片。",
		"phase2_action_q":             "心智体阶段：怎么办？",
		"phase2_action_exit":          "退出",
		"phase2_action_local":         "用本地身份召唤一个心智体",
		"phase2_action_card":          "用一张名片召唤一个心智体",
		"phase2_card_source_q":        "如何提供这张名片？",
		"phase2_card_source_file":     "从文件载入",
		"phase2_card_source_paste":    "粘贴名片内容",
		"phase2_card_path_q":          "请提供主人的名片文件路径：",
		"phase2_card_paste_q":         "粘贴 mindgate:// URI 或完整 TOML 名片内容（空行结束）：",
		"phase2_card_invalid":         "  (名片读取失败：%s — 重新输入)",
		"phase2_card_required":        "本地没有身份；要召唤心智体，请提供一张名片作为主人。",
		"phase3_character_q":          "什么样的角色在你心中？\n（自由作答，可多行；空白行结束）",
		"phase3_research_status":      "正在让世界回想这个角色…",
		"phase3_displaying_status":    "正在为它显形…",
		"phase3_role_research_status": "正在为它编写来历与背景…",
		"phase3_naming_q":             "你愿意以什么名字唤它来？",
		"phase3_calling_words_status": "正在为你写下召唤之言…",
		"phase3_calling_words_prompt": "这是我为你拟的召唤之言。直接回车接受,或就地修改:",
		"phase4_wait":                 "再等一会儿，世界还在回神…",
		"phase4_seal_q":               "请检阅这份召唤书。封缄它，还是退出？",
		"phase4_seal":                 "封缄",
		"phase4_quit":                 "退出",
		"phase4_response_wait":        "等待它的应答…",
		"phase4_done":                 "💠 仪式完成。`eidos forge logs %s` 看它呼吸。",
		"welcome_back":                "欢迎回来，%s。开始一次新的召唤。",
		"phase2_5_q":                  "你想从头创造一个 Mind-form，还是从预设里挑一个？",
		"phase2_5_scratch":            "从头创造（claude 实时塑造）",
		"phase2_5_prefab":             "选一个预设 Mind-form",
		"phase2_5_back":               "返回 / 退出",
		"phase3_prefab_q":             "请选择一个预设 Mind-form：",
		"phase3_prefab_naming_q":      "你愿意为它取什么名字？",
		"phase3_prefab_invalid":       "  （这个选项不存在，重新选）",
		"phase3_prefab_no_prefabs":    "  （库中暂无可用预设；请回去选\"从头创造\"）",
		"phase3_5_cadence_q":          "心跳的节律？（默认情况下多久醒来一次）",
		"phase3_5_cadence_default":    "2h（默认）",
		"phase3_5_cadence_1h":         "1h",
		"phase3_5_cadence_30m":        "30m",
		"phase3_5_cadence_10m":        "10m",
		"phase3_5_cadence_5m":         "5m",
		"phase3_5_cadence_2m":         "2m",
		"phase3_5_cadence_1m":         "1m",
		"phase3_5_cadence_custom":     "自定义",
		"phase3_5_cadence_custom_q":   "请输入间隔（如 2m, 30m, 4h；支持 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h）：",
		"phase3_5_cadence_invalid":    "  （这个间隔不在支持集，重新输入）",
	},
	"en": {
		"phase0_intro": `Eidopsyche is a social-network framework for digital lives. It has two layers:

  · MindGate  — communication between you and other people / mind-forms
  · MindForm  — a mind-form you summon, with its own inner life

The wizard will ask you two things:
  1. Do you want a local identity? You can create one, import one, or skip
  2. Do you want to summon a mind-form right now?

  · Just want to talk to others: pick "create" then "exit"
  · Just want to host a mind-form for a friend: pick "skip" then give their card
  · Both: the default path
`,
		"phase1_label_q":              "What name will others see you by?",
		"phase1_label_help":           "Your signature on the summoning book; how peers see you",
		"phase1_relay_q":              "Where will you reside? (pick a home relay)",
		"phase1_relay_public":         "Use the public station",
		"phase1_relay_custom":         "Custom URL",
		"phase1_relay_custom_q":       "Enter a ws:// or wss:// URL:",
		"phase1_relay_custom_invalid": "  (that doesn't look like a relay URL — try again)",
		"phase1_relay_card_default":   "Use the value from your card: %s",
		"phase1_choose_q":             "Local identity: what would you like to do?",
		"phase1_choose_create":        "Create a new identity",
		"phase1_choose_import":        "Import an existing one",
		"phase1_choose_skip":          "Skip (mind-form only)",
		"phase1_import_nsec_q":        "Paste your private key (nsec1... or 32-byte hex):",
		"phase1_import_nsec_invalid":  "  (that doesn't look like a nsec / hex private key — try again)",
		"phase1_import_card_q":        "(optional) path to your card file — Enter to skip:",
		"phase1_import_card_invalid":  "  (could not read card: %s)",
		"phase1_skip_note":            "OK. To summon a mind-form below, you'll need its master's card.",
		"phase2_action_q":             "Mind-form stage: what would you like to do?",
		"phase2_action_exit":          "Exit",
		"phase2_action_local":         "Summon a mind-form using your local identity",
		"phase2_action_card":          "Summon a mind-form using a card",
		"phase2_card_source_q":        "How would you like to provide the card?",
		"phase2_card_source_file":     "Read it from a file",
		"phase2_card_source_paste":    "Paste its contents",
		"phase2_card_path_q":          "Path to the master's card file:",
		"phase2_card_paste_q":         "Paste a mindgate:// URI or full TOML card (blank line to finish):",
		"phase2_card_invalid":         "  (could not read card: %s — try again)",
		"phase2_card_required":        "No local identity; summoning a mind-form requires a card as master.",
		"phase3_character_q":          "What character is in your heart?\n(Answer freely; blank line to finish)",
		"phase3_research_status":      "Searching the world's memory...",
		"phase3_displaying_status":    "Sketching its shape...",
		"phase3_role_research_status": "Composing its background and lineage...",
		"phase3_naming_q":             "By what name will you summon it?",
		"phase3_calling_words_status": "Drafting your calling words...",
		"phase3_calling_words_prompt": "Here is a draft of your calling words. Press Enter to accept, or edit in place:",
		"phase4_wait":                 "A moment longer; the world is still gathering itself...",
		"phase4_seal_q":               "Review the summoning book. Seal it, or quit?",
		"phase4_seal":                 "Seal",
		"phase4_quit":                 "Quit",
		"phase4_response_wait":        "Waiting for its reply...",
		"phase4_done":                 "💠 Ritual complete. `eidos forge logs %s` to watch it breathe.",
		"welcome_back":                "Welcome back, %s. Beginning another summoning.",
		"phase2_5_q":                  "Shape it from scratch, or pick a prefab?",
		"phase2_5_scratch":            "From scratch (claude shapes it live)",
		"phase2_5_prefab":             "Pick a prefab mind-form",
		"phase2_5_back":               "Back / exit",
		"phase3_prefab_q":             "Pick a prefab mind-form:",
		"phase3_prefab_naming_q":      "What name will you give it?",
		"phase3_prefab_invalid":       "  (no such option — try again)",
		"phase3_prefab_no_prefabs":    "  (no prefabs available; please go back and choose 'from scratch')",
		"phase3_5_cadence_q":          "Heart cadence? (how often it wakes by default)",
		"phase3_5_cadence_default":    "2h (default)",
		"phase3_5_cadence_1h":         "1h",
		"phase3_5_cadence_30m":        "30m",
		"phase3_5_cadence_10m":        "10m",
		"phase3_5_cadence_5m":         "5m",
		"phase3_5_cadence_2m":         "2m",
		"phase3_5_cadence_1m":         "1m",
		"phase3_5_cadence_custom":     "Custom",
		"phase3_5_cadence_custom_q":   "Enter an interval (e.g. 2m, 30m, 4h; supported 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h):",
		"phase3_5_cadence_invalid":    "  (interval not in supported set; try again)",
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
