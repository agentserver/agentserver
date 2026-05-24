import type { Dicts } from '../../lib/i18n'

export const homeStrings: Dicts = {
  zh: {
    // nav
    'nav.brand': 'agentserver',
    'nav.why': '卖点',
    'nav.how': '上手',
    'nav.compare': '对比',
    'nav.signin': '登录 →',
    'nav.lang.toggle': 'EN',

    // hero
    'hero.h1': '你的个人算力网。',
    'hero.sub': 'agentserver 把笔记本、云沙箱、家里的服务器编成一个工作区——从浏览器、命令行，或微信，一并指挥。',
    'hero.cta.primary': '▸ 登录',
    'hero.cta.secondary': '在 GitHub 上查看',
    'hero.term.macbook': 'office-macbook · codex · 10:14',
    'hero.term.sandbox': 'session#a3f · paused 12:30',
    'hero.chat.title': '我自己 · 微信 18:42',
    'hero.chat.user': '上午让你改的 loss 函数跑通了吗？',
    'hero.chat.bot1': '📎 续上会话 session#a3f（office-macbook · 6h 前）',
    'hero.chat.bot2': '🔨 跑 pytest tests/test_loss.py …',
    'hero.chat.bot3': '✓ 17/17 通过 · diff 已推到 feature/weighted-loss',
    'hero.chip.online': '在线',

    // quote
    'quote.body': '"Once you juggle 10+ agents across machines, you stop being a conductor and become an orchestrator."',
    'quote.attrib': '— Addy Osmani · Director, Google Gemini & Cloud AI',
    'quote.caption': 'agentserver = 那个 orchestrator。',

    // pillars
    'pillars.heading': '为什么用 agentserver',
    'pillars.session.title': '同一段会话，任意设备',
    'pillars.session.body': 'codex 会话本身跑在服务端。早上在笔电上没改完的任务，地铁里用微信续上同一段对话继续推进——换的不是命令的目的地，是 agent 的前端。',
    'pillars.pocket.title': '装在口袋里的指挥台',
    'pillars.pocket.body': '微信里一句中文，落到任意设备执行，结果推回同一个聊天框。Telegram 同样支持。',
    'pillars.workspace.title': '一个工作区，所有设备',
    'pillars.workspace.body': '云沙箱、本地机器、IM-bound 代理——全在同一份注册表里，并排出现在 Web UI 上。',
    'pillars.tunnel.title': '只要有网就能接入',
    'pillars.tunnel.body': '本地的 codex / Claude Code / opencode 通过 WebSocket 拨号入网，呈现为一个沙箱。不需要公网 IP、不需要开放端口。',
    'pillars.also': '+ 暂停 / 恢复沙箱 · Jupyter 笔记本 · 多人协作 · 凭证代理 · 操作审计 · SSO（GitHub / OIDC）· 自托管',

    // compare
    'compare.heading': '和已有的工具相比',
    'compare.col.tool': '产品',
    'compare.col.local': '本地代理',
    'compare.col.cloud': '云沙箱',
    'compare.col.peer': '跨设备组网',
    'compare.col.session': '会话跨终端',
    'compare.col.chat': 'IM 通道',
    'compare.caption': '唯一同时勾上五列的产品。',

    // onboarding
    'onb.heading': '7 步，把你的设备连上来',
    'onb.s1.title': '注册',
    'onb.s1.body': '邮箱 / GitHub / SSO 任选',
    'onb.s2.title': '链接模型账号',
    'onb.s2.body': '自带 ChatGPT / Anthropic 凭证，或选平台托管账号',
    'onb.s3.title': '入网设备',
    'onb.s3.body': 'brew install codex → 粘贴注册码',
    'onb.s4.title': '(可选) 选一台"指挥机"',
    'onb.s4.body': '通常是你的主力笔电；如果只用微信指挥，这步可以跳过',
    'onb.s5.title': '(可选) 开 Jupyter',
    'onb.s5.body': '想手写代码？ctx 已预注入 kernel',
    'onb.s6.title': '绑定微信',
    'onb.s6.body': '扫码即可，从此聊天框 = 终端',
    'onb.s7.title': '邀请协作者',
    'onb.s7.body': '角色：owner / maintainer / developer / guest',
    'onb.docs': '完整文档 →',

    // final cta
    'cta.heading': '把你的设备，编进同一张算力网。',
    'cta.primary': '▸ 登录',
    'cta.secondary': '在自己的域名上自托管',

    // footer
    'footer.col1.title': 'agentserver',
    'footer.col2.title': '社区',
    'footer.col2.weixin': '微信群',
    'footer.col2.telegram': 'Telegram',
    'footer.col2.issues': 'Issue 跟踪',
    'footer.col3.title': '法律',
    'footer.col3.license': 'Apache-2.0',
    'footer.col3.privacy': '隐私',
    'footer.col3.contact': '联系',
  },
  en: {
    // nav
    'nav.brand': 'agentserver',
    'nav.why': 'Why',
    'nav.how': 'How',
    'nav.compare': 'Compare',
    'nav.signin': 'Sign in →',
    'nav.lang.toggle': '中',

    // hero
    'hero.h1': 'Your Personal Computility.',
    'hero.sub': 'agentserver weaves laptops, cloud sandboxes, and home servers into one workspace — commanded from your browser, your CLI, or your WeChat chat.',
    'hero.cta.primary': '▸ Sign in',
    'hero.cta.secondary': 'View on GitHub',
    'hero.term.macbook': 'office-macbook · codex · 10:14',
    'hero.term.sandbox': 'session#a3f · paused 12:30',
    'hero.chat.title': 'me · WeChat 18:42',
    'hero.chat.user': 'Did the loss-fn refactor we started this morning land?',
    'hero.chat.bot1': '📎 resumed session#a3f (office-macbook · 6h ago)',
    'hero.chat.bot2': '🔨 running pytest tests/test_loss.py …',
    'hero.chat.bot3': '✓ 17/17 passed · pushed to feature/weighted-loss',
    'hero.chip.online': 'online',

    // quote
    'quote.body': '"Once you juggle 10+ agents across machines, you stop being a conductor and become an orchestrator."',
    'quote.attrib': '— Addy Osmani · Director, Google Gemini & Cloud AI',
    'quote.caption': 'agentserver is that orchestrator.',

    // pillars
    'pillars.heading': 'Why agentserver',
    'pillars.session.title': 'One session, any device',
    'pillars.session.body': 'codex sessions live on the server, not the laptop they started from. Pause a half-finished refactor at the office; pick up the same conversation from WeChat on the subway home. The front-end moves; the agent does not.',
    'pillars.pocket.title': 'Pocket-sized command line',
    'pillars.pocket.body': 'One sentence in WeChat lands on the right device. The result comes back to the same chat. Telegram supported too.',
    'pillars.workspace.title': 'One workspace, every device',
    'pillars.workspace.body': 'Cloud sandboxes, local machines, IM-bound agents — all in one registry, side by side in the Web UI.',
    'pillars.tunnel.title': 'If it can reach the internet, it can join.',
    'pillars.tunnel.body': 'Your local codex / Claude Code / opencode dials home over WebSocket and shows up as a sandbox. No public IP, no open ports.',
    'pillars.also': '+ Pausable sandboxes · Jupyter notebook · Multi-user · Credential proxy · Audit log · SSO (GitHub / OIDC) · Self-host',

    // compare
    'compare.heading': 'How it differs',
    'compare.col.tool': 'Tool',
    'compare.col.local': 'local',
    'compare.col.cloud': 'cloud',
    'compare.col.peer': 'peer',
    'compare.col.session': 'session',
    'compare.col.chat': 'chat',
    'compare.caption': 'The only one with all five checked.',

    // onboarding
    'onb.heading': '7 steps to wire it all up',
    'onb.s1.title': 'Register',
    'onb.s1.body': 'Email / GitHub / SSO — your pick',
    'onb.s2.title': 'Link a model account',
    'onb.s2.body': 'Bring your own ChatGPT / Anthropic credential, or use a managed one',
    'onb.s3.title': 'Enroll devices',
    'onb.s3.body': 'brew install codex → paste the registration code',
    'onb.s4.title': '(Optional) Pick a "command machine"',
    'onb.s4.body': 'Usually your daily-driver laptop; skip if you only drive from IM',
    'onb.s5.title': '(Optional) open Jupyter',
    'onb.s5.body': 'Prefer hand-written code? ctx is pre-injected in every kernel',
    'onb.s6.title': 'Bind WeChat',
    'onb.s6.body': 'Scan the QR. From now on, the chat window is your terminal',
    'onb.s7.title': 'Invite collaborators',
    'onb.s7.body': 'Roles: owner / maintainer / developer / guest',
    'onb.docs': 'Full docs →',

    // final cta
    'cta.heading': 'Weave your devices into one Computility.',
    'cta.primary': '▸ Sign in',
    'cta.secondary': 'Self-host on your own domain',

    // footer
    'footer.col1.title': 'agentserver',
    'footer.col2.title': 'Community',
    'footer.col2.weixin': 'WeChat group',
    'footer.col2.telegram': 'Telegram',
    'footer.col2.issues': 'Issue tracker',
    'footer.col3.title': 'Legal',
    'footer.col3.license': 'Apache-2.0',
    'footer.col3.privacy': 'Privacy',
    'footer.col3.contact': 'Contact',
  },
}
