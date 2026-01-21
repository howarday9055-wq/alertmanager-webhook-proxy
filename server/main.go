package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	FeishuWebhookURL string
	TemplateID	   string
	TemplateVersion  string
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

// getEnvOrDefault retrieves the value of the environment variable named by the key.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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

func filterMap(m map[string]string, excludeKeys []string) map[string]string {
	excludeSet := make(map[string]struct{})
	for _, key := range excludeKeys {
		excludeSet[key] = struct{}{}
	}

	newMap := make(map[string]string)
	for k, v := range m {
		if _, found := excludeSet[k]; !found {
			newMap[k] = v
		}
	}
	return newMap
}

func mapToString(m map[string]string) string {
	var pairs []string
	for k, v := range m {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, ", ")
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

	// Construct job_name
	var job_name string
	value, ok := alert.Labels["job"]
	if ok && value != "" {
		job_name = fmt.Sprintf("(%s)", value)
	} else {
		job_name = ""
	}

	// Exclude certain keys from labels for label_values
	excludeKeys := []string{"alertname", "severity", "prometheus"}
	labelsMap := filterMap(alert.Labels, excludeKeys)
	label_values := mapToString(labelsMap)

	// Construct the template variables
	templateVars := map[string]string{
		"status":       	alert.Status,
		"card_style":  		cardStyle,
		"alert_count": 		fmt.Sprintf("%d", alert_count),
		"job_name":     	job_name,
		"alert_name":   	getStringOrDefault(alert.Labels, "alertname", "未知告警"),
		"severity":     	getStringOrDefault(alert.Labels, "severity", "warning"),
		"label_values":  	label_values,
		"summary":      	getStringOrDefault(alert.Annotations, "summary", "无摘要"),
		"description":  	getStringOrDefault(alert.Annotations, "description", "无详细描述"),
		"start_time":   	formatTimeRFC3339ToLocal(alert.StartsAt),
		"end_time":     	formatTimeRFC3339ToLocal(alert.EndsAt),
	}

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
	// Load Feishu webhook configuration from environment variables
	FeishuWebhookURL = getEnvOrDefault(
		"FEISHU_WEBHOOK_URL", 
		"https://open.feishu.cn/open-apis/bot/v2/hook/eaa5004a-1887-4c55-9473-796ae0870537",
	)
	TemplateID = getEnvOrDefault("FEISHU_TEMPLATE_ID", "AAqxWGzCuibu4")
	TemplateVersion = getEnvOrDefault("FEISHU_TEMPLATE_VERSION", "1.0.5")
	Port := getEnvOrDefault("PORT", "5000")

	log.Printf("Configuration loaded:")
	log.Printf("  Webhook URL: %s", FeishuWebhookURL)
	log.Printf("  Template ID: %s", TemplateID)
	log.Printf("  Template Version: %s", TemplateVersion)

	// Initialize Gin router
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

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
			log.Printf("Alert details: ")
			log.Printf("  - Status: %s", alert.Status)
			log.Printf("  - Labels: %+v", alert.Labels)
			log.Printf("  - Annotations: %+v", alert.Annotations)

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
				log.Printf("Successfully sent alert to Feishu: alertname=%s", 
					getStringOrDefault(alert.Labels, "alertname", "unknown"))
			}
		}

		if len(sendErrs) > 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "partial_error", "errors": sendErrs})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "alerts processed successfully"})
	})

	log.Printf("Starting Alertmanager webhook server on port %s...", Port)
	log.Fatal(r.Run(":" + Port))
}