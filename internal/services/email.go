package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
	mailjet "github.com/mailjet/mailjet-apiv3-go"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/emaildataplane"
	"github.com/resend/resend-go/v2"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

const (
	ProviderSendGrid EmailProvider = "sendgrid"
	ProviderAWSSES   EmailProvider = "ses"
	ProviderOCIEmail EmailProvider = "oci"
	ProviderMailJet  EmailProvider = "mailjet"
	ProviderResend   EmailProvider = "resend"
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
	OCIConfigProvider  common.ConfigurationProvider
	OCICompartmentID   string
	OCIEndpointSuffix  string
	MailJetAPIKey      string
	MailJetSecretKey   string
	ResendAPIKey       string
}

type EmailAttachment struct {
	FileName    string
	ContentType string
	Data        []byte
}

func NewEmailService(
	provider EmailProvider,
	fromEmail, fromName string,
	defaultTos []string,
	sendGridAPIKey string,
	awsRegion string,
	awsAccessKeyID string,
	awsSecretAccessKey string,
	ociConfigPath string,
	ociProfileName string,
	ociCompartmentID string,
	ociEndpointSuffix string,
	mailJetAPIKey string,
	mailJetSecretKey string,
	resendAPIKey string,
) *EmailService {
	isInitialized := false

	var ociConfigProvider common.ConfigurationProvider

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
	case ProviderOCIEmail:
		if ociConfigPath != "" {
			ociConfigProvider = common.CustomProfileConfigProvider(ociConfigPath, ociProfileName)
		} else {
			ociConfigProvider = common.DefaultConfigProvider()
		}
		_, err := ociConfigProvider.TenancyOCID()
		if err != nil {
			log.Println("Warning: OCI email service not properly initialized. Missing OCI credentials.")
		}
		isInitialized = err == nil && ociCompartmentID != "" && fromEmail != ""

		if !isInitialized {
			log.Println("Warning: OCI email service not properly initialized. Missing OCI compartment ID or sender email.")
			if err != nil {
				log.Printf("OCI configuration error: %v", err)
			}
		}
	case ProviderMailJet:
		isInitialized = mailJetAPIKey != "" && mailJetSecretKey != "" && fromEmail != ""
		if !isInitialized {
			log.Println("Warning: MailJet email service not properly initialized. Missing MailJet API keys or sender email.")
		}
	case ProviderResend:
		isInitialized = resendAPIKey != "" && fromEmail != ""
		if !isInitialized {
			log.Println("Warning: Resend email service not properly initialized. Missing API key or sender email.")
		}

	default:
		log.Println("Warning: Unknown email provider specified.")
	}

	if !isInitialized {
		log.Println("Warning: Email service not fully initialized. Missing API key or sender email.")
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
		OCIConfigProvider:  ociConfigProvider,
		OCICompartmentID:   ociCompartmentID,
		OCIEndpointSuffix:  ociEndpointSuffix,
		MailJetAPIKey:      mailJetAPIKey,
		MailJetSecretKey:   mailJetSecretKey,
		ResendAPIKey:       resendAPIKey,
	}
}

func (s *EmailService) SendEmailWithAttachment(
	subject, body string,
	to, cc []string,
	attachment *EmailAttachment,
) error {
	if !s.IsInitialized {
		return fmt.Errorf("email service not properly initialized")
	}

	switch s.Provider {
	case ProviderSendGrid:
		return s.sendWithSendGrid(to, cc, subject, body, attachment)
	case ProviderAWSSES:
		return s.sendWithAWSSES(to, cc, subject, body, attachment)
	case ProviderOCIEmail:
		return s.sendWithOCIEmail(to, cc, subject, body, attachment)
	case ProviderMailJet:
		return s.sendWithMailJet(to, cc, subject, body, attachment)
	case ProviderResend:
		return s.sendWithResend(to, cc, subject, body, attachment)
	default:
		return fmt.Errorf("unknown email provider: %s", s.Provider)
	}
}

func (s *EmailService) sendWithSendGrid(
	to []string,
	cc []string,
	subject string,
	body string,
	attachment *EmailAttachment,
) error {
	from := mail.NewEmail(s.FromName, s.FromEmail)

	// Create personalization
	personalization := mail.NewPersonalization()

	// Add TO recipients
	for _, recipient := range to {
		personalization.AddTos(mail.NewEmail("", recipient))
	}

	// Add CC recipients
	for _, recipient := range cc {
		personalization.AddCCs(mail.NewEmail("", recipient))
	}

	// Create email
	m := mail.NewV3Mail()
	m.SetFrom(from)
	m.Subject = subject
	m.AddPersonalizations(personalization)

	// Add content
	content := mail.NewContent("text/html", body)
	m.AddContent(content)

	// Add attachment if provided
	if attachment != nil {
		a := mail.NewAttachment()
		a.SetFilename(attachment.FileName)
		a.SetType(attachment.ContentType)
		encoded := base64.StdEncoding.EncodeToString(attachment.Data)
		a.SetContent(encoded)
		a.SetDisposition("attachment")
		m.AddAttachment(a)
	}

	// Send email
	request := sendgrid.GetRequest(s.SendGridAPIKey, "/v3/mail/send", "https://api.sendgrid.com")
	request.Method = "POST"
	request.Body = mail.GetRequestBody(m)

	response, err := sendgrid.API(request)
	if err != nil {
		return fmt.Errorf("SendGrid API error: %v", err)
	}

	if response.StatusCode >= 300 {
		return fmt.Errorf("SendGrid API returned status %d: %s", response.StatusCode, response.Body)
	}

	log.Printf("Email sent successfully via SendGrid to: %v", to)
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
		return fmt.Errorf("failed to create AWS session: %v", err)
	}

	// Create SES service client
	svc := ses.New(sess)

	// Prepare destinations
	var destinations []*string
	for _, recipient := range to {
		destinations = append(destinations, aws.String(recipient))
	}
	for _, recipient := range cc {
		destinations = append(destinations, aws.String(recipient))
	}

	// If we have an attachment, we need to send raw email
	if attachment != nil {
		return s.sendRawEmailWithSES(svc, to, cc, subject, body, attachment)
	}

	// Simple email without attachment
	input := &ses.SendEmailInput{
		Destination: &ses.Destination{
			ToAddresses: aws.StringSlice(to),
			CcAddresses: aws.StringSlice(cc),
		},
		Message: &ses.Message{
			Body: &ses.Body{
				Html: &ses.Content{
					Charset: aws.String("UTF-8"),
					Data:    aws.String(body),
				},
			},
			Subject: &ses.Content{
				Charset: aws.String("UTF-8"),
				Data:    aws.String(subject),
			},
		},
		Source: aws.String(s.FromEmail),
	}

	_, err = svc.SendEmail(input)
	if err != nil {
		return fmt.Errorf("failed to send email via AWS SES: %v", err)
	}

	log.Printf("Email sent successfully via AWS SES to: %v", to)
	return nil
}

func (s *EmailService) sendRawEmailWithSES(svc *ses.SES, to, cc []string, subject, body string, attachment *EmailAttachment) error {
	// Build raw email message
	var buffer bytes.Buffer

	// Headers
	buffer.WriteString(fmt.Sprintf("From: %s <%s>\r\n", s.FromName, s.FromEmail))
	buffer.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	if len(cc) > 0 {
		buffer.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", ")))
	}
	buffer.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buffer.WriteString("MIME-Version: 1.0\r\n")
	buffer.WriteString("Content-Type: multipart/mixed; boundary=\"boundary123\"\r\n")
	buffer.WriteString("\r\n")

	// Email body
	buffer.WriteString("--boundary123\r\n")
	buffer.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buffer.WriteString("\r\n")
	buffer.WriteString(body)
	buffer.WriteString("\r\n")

	// Attachment
	buffer.WriteString("--boundary123\r\n")
	buffer.WriteString(fmt.Sprintf("Content-Type: %s\r\n", attachment.ContentType))
	buffer.WriteString("Content-Transfer-Encoding: base64\r\n")
	buffer.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", attachment.FileName))
	buffer.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString(attachment.Data)
	buffer.WriteString(encoded)
	buffer.WriteString("\r\n")
	buffer.WriteString("--boundary123--\r\n")

	// Prepare destinations
	var destinations []*string
	for _, recipient := range to {
		destinations = append(destinations, aws.String(recipient))
	}
	for _, recipient := range cc {
		destinations = append(destinations, aws.String(recipient))
	}

	input := &ses.SendRawEmailInput{
		RawMessage: &ses.RawMessage{
			Data: buffer.Bytes(),
		},
		Destinations: destinations,
		Source:       aws.String(s.FromEmail),
	}

	_, err := svc.SendRawEmail(input)
	if err != nil {
		return fmt.Errorf("failed to send raw email via AWS SES: %v", err)
	}

	return nil
}

func (s *EmailService) sendWithOCIEmail(
	to []string,
	cc []string,
	subject string,
	body string,
	attachment *EmailAttachment,
) error {
	client, err := emaildataplane.NewEmailDPClientWithConfigurationProvider(s.OCIConfigProvider)
	if err != nil {
		return fmt.Errorf("failed to create OCI email client: %v", err)
	}

	// Prepare recipients
	var toAddresses []emaildataplane.EmailAddress
	for _, recipient := range to {
		toAddresses = append(toAddresses, emaildataplane.EmailAddress{Email: &recipient})
	}

	var ccAddresses []emaildataplane.EmailAddress
	for _, recipient := range cc {
		ccAddresses = append(ccAddresses, emaildataplane.EmailAddress{Email: &recipient})
	}

	recipients := &emaildataplane.Recipients{
		To: toAddresses,
	}
	if len(cc) > 0 {
		recipients.Cc = ccAddresses
	}

	sender := &emaildataplane.Sender{
		CompartmentId: &s.OCICompartmentID,
		SenderAddress: &emaildataplane.EmailAddress{
			Email: &s.FromEmail,
			Name:  &s.FromName,
		},
	}

	submitDetails := emaildataplane.SubmitEmailDetails{
		Subject:    &subject,
		BodyHtml:   &body,
		Recipients: recipients,
		Sender:     sender,
	}

	// Add attachment handling here if needed - OCI email attachments are complex
	// For now, we'll send without attachments or implement raw email
	if attachment != nil {
		log.Printf("Warning: OCI Email attachments not fully implemented in this version")
	}

	submitRequest := emaildataplane.SubmitEmailRequest{
		SubmitEmailDetails: submitDetails,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = client.SubmitEmail(ctx, submitRequest)
	if err != nil {
		return fmt.Errorf("failed to send email via OCI: %v", err)
	}

	log.Printf("Email sent successfully via OCI Email to: %v", to)
	return nil
}

func (s *EmailService) sendWithMailJet(
	to []string,
	cc []string,
	subject string,
	body string,
	attachment *EmailAttachment,
) error {
	mj := mailjet.NewMailjetClient(s.MailJetAPIKey, s.MailJetSecretKey)

	// Prepare TO recipients
	var recipientsTo mailjet.RecipientsV31
	for _, recipient := range to {
		recipientsTo = append(recipientsTo, mailjet.RecipientV31{
			Email: recipient,
		})
	}

	// Prepare CC recipients
	var recipientsCc mailjet.RecipientsV31
	for _, recipient := range cc {
		recipientsCc = append(recipientsCc, mailjet.RecipientV31{
			Email: recipient,
		})
	}

	// Create message
	message := mailjet.InfoMessagesV31{
		From: &mailjet.RecipientV31{
			Email: s.FromEmail,
			Name:  s.FromName,
		},
		To:       &recipientsTo,
		Cc:       &recipientsCc,
		Subject:  subject,
		HTMLPart: body,
	}

	// Add attachment if provided
	if attachment != nil {
		encoded := base64.StdEncoding.EncodeToString(attachment.Data)
		message.Attachments = &mailjet.AttachmentsV31{
			{
				ContentType:   attachment.ContentType,
				Filename:      attachment.FileName,
				Base64Content: encoded,
			},
		}
	}

	messages := mailjet.MessagesV31{Info: []mailjet.InfoMessagesV31{message}}

	_, err := mj.SendMailV31(&messages)
	if err != nil {
		return fmt.Errorf("failed to send email via MailJet: %v", err)
	}

	log.Printf("Email sent successfully via MailJet to: %v", to)
	return nil
}

func (s *EmailService) sendWithResend(
	to []string,
	cc []string,
	subject string,
	body string,
	attachment *EmailAttachment,
) error {
	client := resend.NewClient(s.ResendAPIKey)

	// Prepare email parameters
	params := &resend.SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", s.FromName, s.FromEmail),
		To:      to,
		Subject: subject,
		Html:    body,
	}

	// Add CC recipients if any
	if len(cc) > 0 {
		params.Cc = cc
	}

	// Add attachment if provided
	if attachment != nil {
		params.Attachments = []*resend.Attachment{
			{
				Content:  attachment.Data,
				Filename: attachment.FileName,
			},
		}
	}

	sent, err := client.Emails.Send(params)
	if err != nil {
		return fmt.Errorf("failed to send email via Resend: %v", err)
	}

	log.Printf("Email sent successfully via Resend (ID: %s) to: %v", sent.Id, to)
	return nil
}

// IsConfigured returns true if the email service is properly configured
func (s *EmailService) IsConfigured() bool {
	return s.IsInitialized
}

// SendEmail sends a simple email without attachment
func (s *EmailService) SendEmail(subject, body string, to, cc []string) error {
	return s.SendEmailWithAttachment(subject, body, to, cc, nil)
}

// SendEmailToDefaults sends email to default recipients
func (s *EmailService) SendEmailToDefaults(subject, body string, attachment *EmailAttachment) error {
	return s.SendEmailWithAttachment(subject, body, s.DefaultTos, nil, attachment)
}
