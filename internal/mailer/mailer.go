package mailer

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"

	"github.com/wneessen/go-mail"
)

//go:embed templates/*.html
var templatesFS embed.FS

// SMTPSender — implementing email sending via SMTP.
type SMTPSender struct {
	client      *mail.Client
	from        string
	frontendURL string
	templates   map[domain.Template]*template.Template
}

// NewSMTPSender - creates a new instance of 'SMTPSender'.
func NewSMTPSender(cfg config.SMTPConfig, FrontendURL string) (*SMTPSender, error) {
	client, err := mail.NewClient(
		cfg.Host,
		mail.WithPort(cfg.Port),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithUsername(cfg.Username),
		mail.WithPassword(cfg.Password),
		mail.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("create smtp client: %w", err)
	}

	s := &SMTPSender{
		client:      client,
		from:        cfg.From,
		frontendURL: FrontendURL,

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
		return err
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
	data map[string]any,
) error {
	t, ok := s.templates[tmpl]
	if !ok {
		return fmt.Errorf("template not found: %s", tmpl)
	}

	if data == nil {
		data = make(map[string]any)
	}
	data["FrontendURL"] = s.frontendURL

	var html strings.Builder
	if err := t.Execute(&html, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	msg := mail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return err
	}
	if err := msg.To(to); err != nil {
		return err
	}

	msg.Subject(s.getSubject(tmpl))
	msg.SetBodyString(mail.TypeTextHTML, html.String())

	err := s.client.DialAndSendWithContext(ctx, msg)
	if err != nil {
		return err
	}

	return nil
}

func (s *SMTPSender) getSubject(tmpl domain.Template) string {
	subjects := map[domain.Template]string{
		domain.EmailVerify:   "Подтверждение email",
		domain.PasswordReset: "Сброс пароля",
		domain.EmailChange:   "Смена email",
	}
	return subjects[tmpl]
}
