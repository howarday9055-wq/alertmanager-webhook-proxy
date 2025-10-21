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
	FeishuWebhookURL = "https://open.feishu.cn/open-apis/bot/v2/hook/eaa5004a-1887-4c55-9473-796ae0870537"
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
			TemplateVersionName string            `json:"template_version_name"`
			TemplateVariable map[string]string `json:"template_variable"`
		} `json:"data"`
	}	 `json:"card"`
}

// getStringOrDefault safely gets a string from map with default value
func getStringOrDefault(m map[string]string, key, defaultValue string) string {
	if m == nil {
		return defaultValue
	}
	if val, ok := m[key]; ok && val != "" {
		return val
	}
	return defaultValue
}

// formatTimeRFC3339ToLocal converts a RFC3339 time string to local time formatted as "2006-01-02 15:04:05"
func formatTimeRFC3339ToLocal(t string) string {
	log.Printf("Formatting time: %s", t)
	if t == "" || t == "0001-01-01T00:00:00Z" {
		return "0001-01-01 00:00:00"
	}
	parsedTime, err := time.Parse(time.RFC3339, t)
	if err != nil {
		log.Printf("Failed to parse time %s: %v", t, err)
		return t
	}
	// Convert to local time (UTC+8)
	loc := time.FixedZone("UTC+8", 8*60*60)
	return parsedTime.In(loc).Format("2006-01-02 15:04:05")
}

// buildFeishuPayloadForSingleAlert constructs the Feishu webhook payload for a single alert
func buildFeishuPayloadForSingleAlert(alert Alert, alert_count int) ([]byte, error) {
	var cardStyle string
	switch alert.Status {
	case "firing":
		cardStyle = "red"
	case "resolved":
		cardStyle = "green"
	default:
		cardStyle = "grey"
	}
	// Construct the template variables
	templateVars := map[string]string{
		"status":       alert.Status,
		"card_style":  cardStyle,
		"alert_count": fmt.Sprintf("%d", alert_count),
		"job_name":     getStringOrDefault(alert.Labels, "job", "未知"),
		"alert_name":    getStringOrDefault(alert.Labels, "alertname", "未知告警"),
		"severity":     getStringOrDefault(alert.Labels, "severity", "warning"),
		"instance":     getStringOrDefault(alert.Labels, "instance", "未知实例"),
		"summary":      getStringOrDefault(alert.Annotations, "summary", "无摘要"),
		"description":  getStringOrDefault(alert.Annotations, "description", "无详细描述"),
		"start_time":     formatTimeRFC3339ToLocal(alert.StartsAt),
		"end_time":       formatTimeRFC3339ToLocal(alert.EndsAt),
	}

	// Debug log the template variables
	log.Printf("Template variables: %+v", templateVars)

	payload := FeishuTemplatePayload{
		MsgType: "interactive",
	}
	payload.Card.Type = "template"
	payload.Card.Data.TemplateID = TemplateID
	payload.Card.Data.TemplateVersionName = TemplateVersion
	payload.Card.Data.TemplateVariable = templateVars

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Debug log the payload
	log.Printf("Feishu payload JSON: %s", string(payloadBytes))

	return payloadBytes, nil
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

	// 读取响应body用于调试
	var respBody bytes.Buffer
	respBody.ReadFrom(resp.Body)
	log.Printf("Feishu response status: %d, body: %s", resp.StatusCode, respBody.String())

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu webhook returned non-200 status: %d, body: %s", resp.StatusCode, respBody.String())
	}

	return nil
}

// main function
func main() {
	r := gin.Default()

	r.POST("/proxy", func(c *gin.Context) {
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
		for i, alert := range webhook.Alerts {
			log.Printf("Processing alert %d/%d", i+1, len(webhook.Alerts))
			log.Printf("Alert details - Status: %s, Labels: %+v, Annotations: %+v", 
				alert.Status, alert.Labels, alert.Annotations)

			payload, err := buildFeishuPayloadForSingleAlert(alert, len(webhook.Alerts))
			if err != nil {
				log.Printf("Error building Feishu payload: %v", err)
				sendErrs = append(sendErrs, err.Error())
				continue
			}

			if err := sendToFeishu(payload); err != nil {
				log.Printf("Error sending to Feishu: %v", err)
				sendErrs = append(sendErrs, err.Error())
			} else {
				log.Printf("Successfully sent alert to Feishu: alertname=%s instance=%s", 
					getStringOrDefault(alert.Labels, "alertname", "unknown"),
					getStringOrDefault(alert.Labels, "instance", "unknown"))
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