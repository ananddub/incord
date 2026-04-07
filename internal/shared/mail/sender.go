package mail

import (
	"fmt"
	"net/smtp"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

type Sender struct {
	host string
	port int
	from string
}

func NewSender(cfg config.SMTPConfig) *Sender {
	return &Sender{
		host: cfg.Host,
		port: cfg.Port,
		from: cfg.From,
	}
}

func (s *Sender) SendOTP(to, otp string) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	subject := "Your ndiscord verification code"
	body := fmt.Sprintf("Your verification code is: %s\n\nThis code expires in 5 minutes.", otp)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.from, to, subject, body)

	err := smtp.SendMail(addr, nil, s.from, []string{to}, []byte(msg))
	if err != nil {
		logger.Log.Error().Err(err).Str("to", to).Msg("failed to send OTP email")
		return fmt.Errorf("failed to send email: %w", err)
	}

	logger.Log.Info().Str("to", to).Msg("OTP email sent")
	return nil
}
