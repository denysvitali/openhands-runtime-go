package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// executeBrowseURL navigates to a URL
func (e *Executor) executeBrowseURL(ctx context.Context, action models.BrowseURLAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_url")
	defer span.End()

	e.logger.Infof("Browsing URL: %s", action.URL)

	// Simple HTTP client implementation for basic URL fetching
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", action.URL, nil)
	if err != nil {
		return models.NewBrowserObservation(
			fmt.Sprintf("Failed to create request for %s: %v", action.URL, err),
			action.URL,
			"",
			"browse",
		), nil
	}

	// Set a reasonable User-Agent
	req.Header.Set("User-Agent", "OpenHands-Runtime-Go/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return models.NewBrowserObservation(
			fmt.Sprintf("Failed to fetch %s: %v", action.URL, err),
			action.URL,
			"",
			"browse",
		), nil
	}
	defer resp.Body.Close()

	// Read response body (limit to prevent memory issues)
	const maxBodySize = 1024 * 1024 // 1MB limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return models.NewBrowserObservation(
			fmt.Sprintf("Failed to read response from %s: %v", action.URL, err),
			action.URL,
			"",
			"browse",
		), nil
	}

	content := string(body)
	
	// Basic HTML stripping for better readability (very simple implementation)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		content = e.stripBasicHTML(content)
	}

	result := fmt.Sprintf("Successfully browsed %s (Status: %d)\n\nContent:\n%s", 
		action.URL, resp.StatusCode, content)

	if len(content) >= maxBodySize {
		result += "\n\n[Content truncated - response too large]"
	}

	return models.NewBrowserObservation(
		result,
		action.URL,
		"", // No screenshot in basic implementation
		"browse",
	), nil
}

// executeBrowseInteractive performs browser interaction
func (e *Executor) executeBrowseInteractive(ctx context.Context, action models.BrowseInteractiveAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_interactive")
	defer span.End()

	e.logger.Infof("Interactive browsing with browser ID: %s", action.BrowserID)

	// For now, return a message indicating this is not fully implemented
	// In a full implementation, this would use a headless browser like chromedp
	return models.NewBrowserObservation(
		"Interactive browsing not fully implemented. Consider using browse URL action for basic web content fetching.",
		"",
		"",
		"browse_interactive",
	), nil
}

// stripBasicHTML removes basic HTML tags for better text readability
func (e *Executor) stripBasicHTML(content string) string {
	// Very basic HTML tag removal - not a complete HTML parser
	// Remove script and style tags entirely
	content = removeTagsAndContent(content, "script")
	content = removeTagsAndContent(content, "style")
	
	// Remove common HTML tags but keep content
	tags := []string{"html", "head", "body", "div", "span", "p", "h1", "h2", "h3", "h4", "h5", "h6", 
		"a", "img", "br", "hr", "ul", "ol", "li", "table", "tr", "td", "th", "thead", "tbody"}
	
	for _, tag := range tags {
		content = strings.ReplaceAll(content, "<"+tag+">", "")
		content = strings.ReplaceAll(content, "</"+tag+">", "")
		// Remove tags with attributes
		content = removeTagsWithAttributes(content, tag)
	}
	
	return strings.TrimSpace(content)
}

// removeTagsAndContent removes HTML tags and their content
func removeTagsAndContent(content, tag string) string {
	startTag := "<" + tag
	endTag := "</" + tag + ">"
	
	for {
		start := strings.Index(strings.ToLower(content), strings.ToLower(startTag))
		if start == -1 {
			break
		}
		
		// Find the end of the opening tag
		tagEnd := strings.Index(content[start:], ">")
		if tagEnd == -1 {
			break
		}
		tagEnd += start + 1
		
		// Find the closing tag
		end := strings.Index(strings.ToLower(content[tagEnd:]), strings.ToLower(endTag))
		if end == -1 {
			break
		}
		end += tagEnd + len(endTag)
		
		// Remove the entire tag and its content
		content = content[:start] + content[end:]
	}
	
	return content
}

// removeTagsWithAttributes removes HTML tags that may have attributes
func removeTagsWithAttributes(content, tag string) string {
	// Remove opening tags with attributes like <div class="...">
	for {
		start := strings.Index(strings.ToLower(content), "<"+tag+" ")
		if start == -1 {
			break
		}
		
		end := strings.Index(content[start:], ">")
		if end == -1 {
			break
		}
		end += start + 1
		
		content = content[:start] + content[end:]
	}
	
	return content
}
