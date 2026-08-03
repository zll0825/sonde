package notifier

import (
	"github.com/rs/zerolog/log"
)

// Resolve 按 Telegram → Webhook → Nop 的顺序选择第一个配置就绪的通知通道。
// 返回值恒非 nil：未配置凭据时降级为 NopNotifier 并打启动警告——这是有意
// 设计，告警仍会落库/进 outbox，只是不外推。
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
