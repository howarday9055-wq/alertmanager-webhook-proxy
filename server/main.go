package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Feishu webhook configuration
const (
	FeishuWebhookURL = "https://open.feishu.cn/open-apis/bot/v2/hook/60ca3965-6a3a-483f-b0c3-34ab980d0a29"
	TemplateID	  = "AAqxWGzCuibu4"
	TemplateVersion = "1.0.4"
)

// Alert represents a single alert in the Alertmanager webhook payload
type Alert struct {
	Status	  string            `json:"status"`
	Labels	  map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt   string            `json:"startsAt"`
	EndsAt     string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint string            `json:"fingerprint"`
}

// AlertmanagerWebhook represents the structure of the incoming webhook from Alertmanager
type AlertmanagerWebhook struct {
	Receiver string  `json:"receiver"`
	Status   string  `json:"status"`
	Alerts []Alert `json:"alerts"`
	GroupLabels map[string]string `json:"groupLabels"`
	CommonLabels map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL string `json:"externalURL"`
	Version  string  `json:"version"`
	GroupKey string  `json:"groupKey"`
	TruncatedAlerts int `json:"truncatedAlerts"`
}

// FeishuTemplatePayload represents the payload structure for Feishu webhook with template
type FeishuTemplatePayload struct {
	MsgType string            `json:"msg_type"`
	Card   struct {
		Type   string `json:"type"`
		Data  struct {
			TemplateID      string            `json:"template_id"`
			TemplateVersion string            `json:"template_version"`
			TemplateVariables map[string]string `json:"template_variables"`
		} `json:"data"`
	}	 `json:"card"`
}

// formatTimeRFC3339ToLocal converts a RFC3339 time string to local time formatted as "2006-01-02 15:04:05"
func formatTimeRFC3339ToLocal(t string) string {
	log.Printf("Formatting time: %s", t)
	if t == "" || t == "0001-01-01T00:00:00Z" {
		return "0001-01-01 00:00:00"
	}
	parsedTime, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return t // Return the original string if parsing fails
	}
	// Convert to local time (UTC+8)
	loc := time.FixedZone("UTC+8", 8*60*60)
	return parsedTime.In(loc).Format("2006-01-02 15:04:05")
}

// buildFeishuPayloadForSingleAlert constructs the Feishu webhook payload for a single alert
func buildFeishuPayloadForSingleAlert(alert Alert) ([]byte, error) {
	templateVars := map[string]string{
		"status":       alert.Status,
		"job_name":     alert.Labels["job"],
		"alertname":    alert.Labels["alertname"],
		"severity":     alert.Labels["severity"],
		"instance":     alert.Labels["instance"],
		"summary":      alert.Annotations["summary"],
		"description":  alert.Annotations["description"],
		"startsAt":     formatTimeRFC3339ToLocal(alert.StartsAt),
		"endsAt":       formatTimeRFC3339ToLocal(alert.EndsAt),
		"generatorURL": alert.GeneratorURL,
		"fingerprint":  alert.Fingerprint,
	}

	payload := FeishuTemplatePayload{
		MsgType: "interactive",
	}
	payload.Card.Type = "template"
	payload.Card.Data.TemplateID = TemplateID
	payload.Card.Data.TemplateVersion = TemplateVersion
	payload.Card.Data.TemplateVariables = templateVars

	log.Printf("Built Feishu payload: %+v", payload)

	return json.Marshal(payload)
}

// sendToFeishu sends the given payload to the Feishu webhook URL
func sendToFeishu(payload []byte) error {
	client := &http.Client{Timeout: time.Duration(10) * time.Second}
	req, err := http.NewRequest("POST", FeishuWebhookURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu webhook returned non-200 status: %d", resp.StatusCode)
	}

	return nil
}

// main function
func main() {
	r := gin.Default()

	r.POST("/alert", func(c *gin.Context) {
		var webhook AlertmanagerWebhook
		if err := c.ShouldBindJSON(&webhook); err != nil {
			log.Printf("Error parsing webhook JSON: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		pretty, _ := json.MarshalIndent(webhook, "", "  ")
		log.Printf("Received Alertmanager webhook:\n%s", string(pretty))

		if len(webhook.Alerts) == 0 {
			log.Printf("No alerts in the webhook payload")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "no alerts to process"})
			return
		}

		var sendErrs []string
		for _, alert := range webhook.Alerts {
			payload, err := buildFeishuPayloadForSingleAlert(alert)
			if err != nil {
				log.Printf("Error building Feishu payload: %v", err)
				sendErrs = append(sendErrs, err.Error())
				continue
			}

			if err := sendToFeishu(payload); err != nil {
				log.Printf("Error sending to Feishu: %v", err)
				sendErrs = append(sendErrs, err.Error())
			} else {
				log.Printf("Successfully sent alert to Feishu: alertname=%s instance=%s", alert.Labels["alertname"], alert.Labels["instance"])
			}
		}

		if len(sendErrs) > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "partial_error", "errors": sendErrs})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "alerts processed successfully"})
	})

	log.Println("Starting Alertmanager webhook server on port 5000...")
	log.Fatal(r.Run(":5000"))
}
