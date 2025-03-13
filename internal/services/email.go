package services

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

const (
	ProviderSendGrid EmailProvider = "sendgrid"
	ProviderAWSSES   EmailProvider = "ses"
)

type EmailProvider string

type EmailService struct {
	Provider           EmailProvider
	FromEmail          string
	FromName           string
	DefaultTos         []string
	IsInitialized      bool
	SendGridAPIKey     string
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
}

type EmailAttachment struct {
	FileName    string
	ContentType string
	Data        []byte
}

func NewEmailService(provider EmailProvider, fromEmail, fromName string, defaultTos []string, sendGridAPIKey string, awsRegion string, awsAccessKeyID string, awsSecretAccessKey string) *EmailService {
	isInitialized := false

	switch provider {
	case ProviderSendGrid:
		isInitialized = sendGridAPIKey != "" && fromEmail != ""
		if !isInitialized {
			log.Println("Warning: SendGrid email service not properly initialized. Missing API key or sender email.")
		}
	case ProviderAWSSES:
		isInitialized = awsRegion != "" && awsAccessKeyID != "" && awsSecretAccessKey != "" && fromEmail != ""
		if !isInitialized {
			log.Println("Warning: AWS SES email service not properly initialized. Missing AWS credentials or sender email.")
		}
	default:
		log.Println("Warning: Unknown email provider specified.")
	}

	if !isInitialized {
		log.Println("Warning: Email service not fully initialized. Missing API key od sender email.")
	}

	return &EmailService{
		Provider:           provider,
		FromEmail:          fromEmail,
		FromName:           fromName,
		DefaultTos:         defaultTos,
		IsInitialized:      isInitialized,
		SendGridAPIKey:     sendGridAPIKey,
		AWSRegion:          awsRegion,
		AWSAccessKeyID:     awsAccessKeyID,
		AWSSecretAccessKey: awsSecretAccessKey,
	}
}

func (s *EmailService) SendEmailWithAttachment(subject, body string, to, cc []string, attachment *EmailAttachment) error {
	if !s.IsInitialized {
		return fmt.Errorf("email service not properly initialized")
	}

	switch s.Provider {
	case ProviderSendGrid:
		return s.sendWithSendGrid(to, cc, subject, body, attachment)
	case ProviderAWSSES:
		return s.sendWithAWSSES(to, cc, subject, body, attachment)
	default:
		return fmt.Errorf("unknown email provider: %s", s.Provider)
	}
}

func (s *EmailService) sendWithSendGrid(to []string, cc []string, subject string, body string, attachment *EmailAttachment) error {
	from := mail.NewEmail(s.FromName, s.FromEmail)

	message := mail.NewV3Mail()
	message.SetFrom(from)
	message.Subject = subject

	p := mail.NewPersonalization()
	for _, toEmail := range to {
		p.AddTos(mail.NewEmail("", toEmail))
	}

	for _, ccEmail := range cc {
		p.AddCCs(mail.NewEmail("", ccEmail))
	}
	message.AddPersonalizations(p)

	plainContent := mail.NewContent("text/plain", body)
	message.AddContent(plainContent)

	if attachment != nil {
		a := mail.NewAttachment()
		a.SetContent(base64.StdEncoding.EncodeToString(attachment.Data))
		a.SetType(attachment.ContentType)
		a.SetFilename(attachment.FileName)
		message.AddAttachment(a)
	}

	client := sendgrid.NewSendClient(s.SendGridAPIKey)
	response, err := client.Send(message)
	if err != nil {
		log.Printf("SendgridError: %v", err)
		return err
	}

	if response.StatusCode >= 400 {
		log.Printf("SendGrid error: Status Code: %d, Body: %s", response.StatusCode, response.Body)
		return fmt.Errorf("failed to send email, status code: %d", response.StatusCode)
	}

	log.Printf("Email sent successfully: %d", response.StatusCode)
	return nil
}

func (s *EmailService) sendWithAWSSES(
	to []string,
	cc []string,
	subject string,
	body string,
	attachment *EmailAttachment,
) error {
	// Create AWS session
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(s.AWSRegion),
		Credentials: credentials.NewStaticCredentials(s.AWSAccessKeyID, s.AWSSecretAccessKey, ""),
	})
	if err != nil {
		log.Printf("Failed to create AWS session: %v", err)
		return err
	}

	// Create SES service client
	svc := ses.New(sess)

	// Create raw message
	rawMessage, err := s.createSESRawMessage(to, cc, subject, body, attachment)
	if err != nil {
		return err
	}

	// Send the raw email
	input := &ses.SendRawEmailInput{
		RawMessage: &ses.RawMessage{
			Data: []byte(rawMessage),
		},
	}

	result, err := svc.SendRawEmail(input)
	if err != nil {
		log.Printf("AWS SES error: %v", err)
		return err
	}

	log.Printf("Email sent successfully via AWS SES, MessageID: %s", *result.MessageId)
	return nil
}

func (s *EmailService) createSESRawMessage(to []string, cc []string, subject string, body string, attachment *EmailAttachment) (string, error) {
	// Generate a boundary for the multipart message
	boundary := fmt.Sprintf("Multipart_Boundary_%d", time.Now().UnixNano())

	var message strings.Builder

	// Email headers
	message.WriteString(fmt.Sprintf("From: %s <%s>\r\n", s.FromName, s.FromEmail))
	message.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	if len(cc) > 0 {
		message.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", ")))
	}
	message.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n\r\n", boundary))

	// Text part
	message.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	message.WriteString(body)
	message.WriteString("\r\n\r\n")

	// Attachment part (if provided)
	if attachment != nil {
		message.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		message.WriteString(fmt.Sprintf("Content-Type: %s\r\n", attachment.ContentType))
		message.WriteString("Content-Transfer-Encoding: base64\r\n")
		message.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=%s\r\n\r\n", attachment.FileName))

		// Encode the attachment data as base64
		encodedData := base64.StdEncoding.EncodeToString(attachment.Data)

		// Write the encoded data in lines of 76 characters
		for i := 0; i < len(encodedData); i += 76 {
			end := i + 76
			if end > len(encodedData) {
				end = len(encodedData)
			}
			message.WriteString(encodedData[i:end] + "\r\n")
		}
		message.WriteString("\r\n")
	}

	// Close the MIME boundary
	message.WriteString(fmt.Sprintf("--%s--", boundary))

	return message.String(), nil
}

func (s *EmailService) IsConfigured() bool {
	return s.IsInitialized
}
