package notifier

import (
	"github.com/rs/zerolog/log"
)

// Resolve picks the first configured notifier from Telegram -> Webhook -> Nop.
// Return value is always non-nil: operators without credentials get NopNotifier
// and a warning log, which is intentional (alerts still land in the DB/outbox).
func Resolve() Notifier {
	if t, ok := NewTelegram(); ok {
		log.Info().Msg("notification channel active: Telegram")
		return t
	}
	if w, ok := NewWebhook(); ok {
		log.Info().Msg("notification channel active: webhook")
		return w
	}
	log.Warn().
		Str("hint", "set TELEGRAM_BOT_TOKEN+TELEGRAM_CHAT_ID, or WEBHOOK_URL").
		Msg("no notification channel configured — alerts will only land in DB")
	return NopNotifier{}
}
