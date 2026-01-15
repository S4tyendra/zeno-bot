# Zeno

A modular Telegram bot built with Go.

## Setup

1. Copy `.env.example` to `.env`:
   ```bash
   cp .env.example .env
   ```

2. Edit `.env`:
   ```
   BOT_TOKEN=telegram_bot_token
   MONGODB_URL=mongodb://localhost:27017
   ```

3. Run the bot:
   ```bash
   go run .
   ```

## Project Structure

```
zeno/
├── main.go              # Entry point
├── config/              # Configuration loading
│   └── config.go
├── db/                  # Database connection
│   └── db.go
├── models/              # Data models
│   └── user.go
└── modules/             # Bot modules (commands/features)
    ├── modules.go       # Module registration
    └── aichat/
        └── aichat.go
```



## Adding a New Module

1. Create `modules/newmodule/newmodule.go`:
   ```go
   package newmodule

   import tele "gopkg.in/telebot.v3"

   func Register(b *tele.Bot) {
       b.Handle("/newcommand", handleNewCommand)
   }

   func handleNewCommand(c tele.Context) error {
       return c.Send("Hello from newmodule!")
   }
   ```

2. Register in `modules/modules.go`:
   ```go
   import "zeno/modules/newmodule"

   func RegisterAll(b *tele.Bot) {
       aichat.Register(b)
       newmodule.Register(b)
   }
   ```

## Adding a New Model

Create `models/newmodel.go`:
```go
package models

import "time"

type ChatHistory struct {
    ID        string    `bson:"_id,omitempty"`
    UserID    int64     `bson:"user_id"`
    Message   string    `bson:"message"`
    CreatedAt time.Time `bson:"created_at"`
}
```

Use in modules with `db.Collection("chat_history")`.
