package mailer

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/mail"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"

	gomail "github.com/wneessen/go-mail"
)

var subjects = map[domain.Template]string{
	domain.EmailVerify:   "Подтверждение email",
	domain.PasswordReset: "Сброс пароля",
	domain.EmailChange:   "Смена email",
}

//go:embed templates/*.html
var templatesFS embed.FS

// SMTPSender — implementing email sending via SMTP.
type SMTPSender struct {
	client      *gomail.Client
	from        string
	frontendURL string
	templates   map[domain.Template]*template.Template
}

func newClient(cfg config.SMTPConfig) (*gomail.Client, error) {
	var opts []gomail.Option
	opts = append(opts,
		gomail.WithPort(cfg.Port),
		gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
		gomail.WithUsername(cfg.Username),
		gomail.WithPassword(cfg.Password),
		gomail.WithTimeout(cfg.Timeout),
	)

	if cfg.TLS {
		switch cfg.Port {
		case 465:
			opts = append(opts,
				gomail.WithTLSPolicy(gomail.TLSMandatory),
				gomail.WithSSL(),
			)
		case 587, 25:
			opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
		default:
			opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
		}
	} else {
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	}

	return gomail.NewClient(cfg.Host, opts...)
}

// NewSMTPSender - creates a new instance of 'SMTPSender'.
func NewSMTPSender(cfg config.SMTPConfig, frontendURL string) (*SMTPSender, error) {
	if err := validateSMTPConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid SMTP config: %w", err)
	}

	client, err := newClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create smtp client: %w", err)
	}

	if frontendURL == "" {
		return nil, fmt.Errorf("create smtp client: 'frontendURL' is empty")
	}

	s := &SMTPSender{
		client:      client,
		from:        cfg.From,
		frontendURL: frontendURL,

		templates: make(map[domain.Template]*template.Template),
	}

	if err := s.loadTemplates(); err != nil {
		return nil, fmt.Errorf("load templates: %w", err)
	}

	return s, nil
}

func (s *SMTPSender) loadTemplates() error {
	entries, err := templatesFS.ReadDir("templates")
	if err != nil {
		return fmt.Errorf("read templates directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		tmplName := strings.TrimSuffix(name, ".html")
		tmpl := domain.Template(tmplName)

		t, err := template.New(name).ParseFS(templatesFS, "templates/"+name)
		if err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}

		s.templates[tmpl] = t
	}

	return nil
}

// Send — implementing the 'EmailSender' interface.
func (s *SMTPSender) Send(
	ctx context.Context,
	to string,
	tmpl domain.Template,
	data domain.EmailData,
) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("invalid recipient email: %w", err)
	}

	t, ok := s.templates[tmpl]
	if !ok {
		return fmt.Errorf("template not found: %s", tmpl)
	}

	templateData := map[string]any{
		"Token":       data.Token,
		"Username":    data.Username,
		"Email":       data.Email,
		"NewEmail":    data.NewEmail,
		"FrontendURL": s.frontendURL,
	}

	var html strings.Builder
	if err := t.Execute(&html, templateData); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("set from: %w", err)
	}

	if err := msg.To(to); err != nil {
		return fmt.Errorf("set to: %w", err)
	}

	msg.Subject(s.getSubject(tmpl))
	msg.SetBodyString(gomail.TypeTextHTML, html.String())

	err := s.client.DialAndSendWithContext(ctx, msg)
	if err != nil {
		return err
	}

	return nil
}

func (s *SMTPSender) getSubject(tmpl domain.Template) string {
	subject, ok := subjects[tmpl]
	if !ok {
		return "Уведомление от Linka"
	}
	return subject
}

// validateSMTPConfig - validates SMTP configuration.
func validateSMTPConfig(cfg config.SMTPConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("SMTP host is required")
	}
	if cfg.Port == 0 {
		return fmt.Errorf("SMTP port is required")
	}
	if cfg.From == "" {
		return fmt.Errorf("SMTP from email is required")
	}
	if cfg.Timeout == 0 {
		return fmt.Errorf("SMTP timeout is required")
	}

	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return fmt.Errorf("invalid from email '%s': %w", cfg.From, err)
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid SMTP port: %d (must be between 1 and 65535)", cfg.Port)
	}

	if !isLocalSMTP(cfg.Host) {
		if cfg.Username == "" {
			return fmt.Errorf("SMTP username is required for external server: %s", cfg.Host)
		}
		if cfg.Password == "" {
			return fmt.Errorf("SMTP password is required for external server: %s", cfg.Host)
		}
	}

	if cfg.Timeout < time.Second {
		return fmt.Errorf("SMTP timeout is too small: %v (minimum 1s)", cfg.Timeout)
	}
	if cfg.Timeout > time.Minute {
		return fmt.Errorf("SMTP timeout is too large: %v (maximum 1m)", cfg.Timeout)
	}

	return nil
}

// isLocalSMTP - checks if host is local development SMTP.
func isLocalSMTP(host string) bool {
	localHosts := []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"mailpit",
		"mailhog",
		"smtp",
	}

	for _, local := range localHosts {
		if host == local {
			return true
		}
	}
	return false
}
