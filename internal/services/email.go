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
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

const (
	ProviderSendGrid EmailProvider = "sendgrid"
	ProviderAWSSES   EmailProvider = "ses"
	ProviderOCIEmail EmailProvider = "oci"
	ProviderMailJet  EmailProvider = "mailjet"
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
			log.Println("Watning: MailJet email service not properly initialized. Missing MailJet API keys or sender email.")
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
		OCIConfigProvider:  ociConfigProvider,
		OCICompartmentID:   ociCompartmentID,
		OCIEndpointSuffix:  ociEndpointSuffix,
		MailJetAPIKey:      mailJetAPIKey,
		MailJetSecretKey:   mailJetSecretKey,
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

func (s *EmailService) createSESRawMessage(
	to []string,
	cc []string,
	subject string,
	body string,
	attachment *EmailAttachment,
) (string, error) {
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

func (s *EmailService) sendWithOCIEmail(to, cc []string, subject, body string, attachment *EmailAttachment) error {
	ctx := context.Background()

	client, err := emaildataplane.NewEmailDPClientWithConfigurationProvider(s.OCIConfigProvider)
	if err != nil {
		log.Printf("Error creating OCI Email client: %v", err)
		return err
	}

	toAddresses := make([]emaildataplane.EmailAddress, len(to))
	for i, addr := range to {
		toAddresses[i] = emaildataplane.EmailAddress{
			Email: common.String(addr),
		}
	}

	ccAddresses := make([]emaildataplane.EmailAddress, len(cc))
	for i, addr := range cc {
		ccAddresses[i] = emaildataplane.EmailAddress{
			Email: common.String(addr),
		}
	}

	recipients := &emaildataplane.Recipients{
		To: toAddresses,
	}
	if len(cc) > 0 {
		recipients.Cc = ccAddresses
	}

	sender := &emaildataplane.Sender{
		CompartmentId: common.String(s.OCICompartmentID),
		SenderAddress: &emaildataplane.EmailAddress{
			Email: common.String(s.FromEmail),
			Name:  common.String(s.FromName),
		},
	}

	headerFields := make(map[string]string)

	submitDetails := emaildataplane.SubmitEmailDetails{
		Subject:      common.String(subject),
		BodyText:     common.String(body),
		Recipients:   recipients,
		Sender:       sender,
		HeaderFields: headerFields,
	}

	boundary := "unique-boundary-123"
	mimeBody := bytes.Buffer{}

	// Set MIME headers
	headerFields = map[string]string{
		"Content-Type": fmt.Sprintf("multipart/mixed; boundary=%d", time.Now().UnixNano()),
		"MIME-Version": "1.0",
	}

	mimeBody.WriteString(fmt.Sprintf("\r\n--%s\r\n", boundary))
	mimeBody.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	mimeBody.WriteString("<html><body>Your email content</body></html>\r\n")

	// Add attachment
	mimeBody.WriteString(fmt.Sprintf("\r\n--%s\r\n", boundary))
	mimeBody.WriteString("Content-Type: application/pdf\r\n")
	mimeBody.WriteString("Content-Transfer-Encoding: base64\r\n")
	mimeBody.WriteString("Content-Disposition: attachment; filename=\"document.pdf\"\r\n\r\n")
	mimeBody.WriteString(base64.StdEncoding.EncodeToString(attachment.Data))
	mimeBody.WriteString(fmt.Sprintf("\r\n--%s--", boundary))

	submitDetails.BodyHtml = common.String(mimeBody.String())
	submitDetails.HeaderFields = headerFields

	submitReq := emaildataplane.SubmitEmailRequest{
		SubmitEmailDetails: submitDetails,
	}

	resp, err := client.SubmitEmail(ctx, submitReq)
	if err != nil {
		log.Printf("Error submitting email: %v", err)
		return err
	}

	log.Printf("Email sent successfully via OCI Email service with ID: %s", *resp.MessageId)
	return nil
}

func (s *EmailService) sendWithMailJet(to, cc []string, subject, body string, attachment *EmailAttachment) error {
	mailjetClient := mailjet.NewMailjetClient(s.MailJetAPIKey, s.MailJetSecretKey)

	var toRecipients mailjet.RecipientsV31
	for _, recipient := range to {
		toRecipients = append(toRecipients, mailjet.RecipientV31{
			Email: recipient,
		})
	}

	var ccRecipients mailjet.RecipientsV31
	for _, recipient := range cc {
		ccRecipients = append(ccRecipients, mailjet.RecipientV31{
			Email: recipient,
		})
	}

	messages := mailjet.MessagesV31{
		Info: []mailjet.InfoMessagesV31{
			{
				From: &mailjet.RecipientV31{
					Email: s.FromEmail,
					Name:  s.FromName,
				},
				To:       &toRecipients,
				Cc:       &ccRecipients,
				Subject:  subject,
				TextPart: body,
			},
		},
	}

	if attachment != nil {
		attachmentContent := base64.StdEncoding.EncodeToString(attachment.Data)
		attachments := mailjet.AttachmentsV31{
			{
				ContentType:   attachment.ContentType,
				Filename:      attachment.FileName,
				Base64Content: attachmentContent,
			},
		}
		messages.Info[0].Attachments = &attachments
	}

	response, err := mailjetClient.SendMailV31(&messages)
	if err != nil {
		log.Printf("Error sending email via MailJet: %v", err)
		return err
	}

	if response.ResultsV31 != nil && len(response.ResultsV31) > 0 {
		for _, result := range response.ResultsV31 {
			if result.Status != "success" {
				log.Printf("Email sent via MailJet failed with status: %s", result.Status)
				return fmt.Errorf("failed to send email via MailJet, status: %s", result.Status)
			} else {
				log.Printf("Email sent successfully via MailJet with Status: %s", result.Status)
			}
		}
	}
	return nil
}

func (s *EmailService) IsConfigured() bool {
	return s.IsInitialized
}
