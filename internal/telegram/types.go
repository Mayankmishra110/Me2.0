package telegram

import "encoding/json"

// Update is a subset of Telegram Bot API Update used by this bot.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Message is a subset of Telegram Bot API Message.
type Message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
}

// CallbackQuery is a subset of Telegram Bot API CallbackQuery.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// User is a subset of Telegram Bot API User.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// Chat is a subset of Telegram Bot API Chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// InlineKeyboardMarkup is Telegram's reply_markup for inline buttons.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// InlineKeyboardButton is one inline button.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// apiResponse is the common Bot API envelope.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// sendResult captures message_id from send* responses.
type sendResult struct {
	MessageID int64 `json:"message_id"`
}
