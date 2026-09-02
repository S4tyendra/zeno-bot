package utility

import (
	"github.com/amarnathcjd/gogram/telegram"
)

func handleHelp(m *telegram.NewMessage) error {
	helpText := `🤖 **Zeno Help**

**AI & Search**
• /ask <query or reply> — Gemini (alias: /askai)
• /search <query or reply> — web search (Perplexity)
• @ask <query> — same as /ask
• Reply to the bot — continues the AI chat
• Reply to text/media with /ask or @ask — that message is sent as context

**Downloader** (one task at a time, others queue)
• /download <url> [name] — web file, or reply to Telegram media (aliases: /dl)
  m3u8 / YouTube / Instagram / similar → yt-dlp automatically
• /fastdownload <url> [name] — aria2c 16 connections (aliases: /fastdl, /fdl)
• /ytdlp <url> [name] — YouTube, Instagram, TikTok, or raw .m3u8 (aliases: /ytdl)
• /rename <id> <new_name> — or reply to a completed task
• /upload <id> [auto|video|doc|file|audio|photo] — or reply (alias: /up)
  After a download, buttons pick the upload type
• /tasks — queue and recent jobs (aliases: /queue, /dlqueue)
• /cancel <id> — cancel a queued or running task

**Code**
• /code py|sh <code> — run Python or bash
• Reply to a code message with /code py|sh

**AFK**
• /afk [reason] — mark yourself away
• Send any message (except /afk) to come back

**Utilities**
• /math <expr> — math, units, currency, time
  KB/MB = bytes (1024). Use kbit/mbit for bits.
• /id — your user id (and chat id in groups)
• /help, /start — this list

**Admin**
• /logs [n] — last n container log lines (default 50)
• /allowai — allow this group, or reply to allow that user anywhere
• /noallowai — revoke this group, or reply to revoke that user
• /getmodel — current default / image / highimage models
• /setmodel <default|image|highimage> <model_name>
• /getthinking — stored + effective thinking level, stream flag
• /setthinking <minimal|low|medium|high|max|off>
  max → high. off → minimal (or low if the model rejects minimal)
• /thinkstream on|off — stream Gemini thought headings live
• /sudoers add|remove — this chat gets startup/shutdown broadcasts

**Examples:**
• ` + "`/math 100 USD to INR`" + `
• ` + "`/math 10 km to miles`" + `
• ` + "`/math sqrt(144)`" + `
• ` + "`/search latest tech news`" + `
• ` + "`/ask write a python script to scrape google`" + `
• ` + "`/download https://example.com/file.mp4`" + `
• ` + "`/ytdlp https://youtube.com/watch?v=...`" + `
• ` + "`/setmodel default gemini-3.1-pro`" + `
• ` + "`/setthinking high`" + `
• ` + "`/thinkstream on`" + `
`

	m.Reply(helpText, &telegram.SendOptions{ParseMode: "Markdown"})
	return nil
}
