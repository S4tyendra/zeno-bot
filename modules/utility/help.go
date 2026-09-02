package utility

import (
	"github.com/amarnathcjd/gogram/telegram"
)

func handleHelp(m *telegram.NewMessage) error {
	helpText := `🤖 **Bot Help**

Here are the available commands:

**AI & Search**
• /ask <query or reply> - Ask the AI (Gemini)
• /askai <query or reply> - /ask (alias)
• /search <query or reply> - Search the web (Perplexity)
• @ask <query or reply> - /ask (alias)
• Reply to a message (text or media) with @ask — the reply target is shown to the AI

**Downloader**
• /download <url> or reply to media (alias: /dl)
• /fastdownload <url> - aria2c 16 connections (alias: /fastdl)
• /ytdlp <url> - YouTube, Instagram, or m3u8
• /rename <id> <name> - rename a completed download
• /upload <id> [auto|video|doc|file|audio|photo]
• /tasks - queue  •  /cancel <id>

**Utilities**
• /math <expr> - Math, Unit & Currency conversion
  _(Note: 'KB', 'MB' = Bytes; Use 'kbit', 'mbit' for Bits. All data units are 1024-based)_
• /id - Get your User & Chat ID
• /logs - View system logs (Admin only)

**Examples:**
• ` + "`/math 100 USD to INR`" + `
• ` + "`/math 10 km to miles`" + `
• ` + "`/math sqrt(144)`" + `
• ` + "`/search latest tech news`" + `
• ` + "`/ask write a python script to scrape google`" + `
`

	m.Reply(helpText, &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}
