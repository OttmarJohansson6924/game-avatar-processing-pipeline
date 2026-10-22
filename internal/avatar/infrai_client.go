package avatar

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type imageData struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type InfraiClient struct {
	baseURL string
	key     string
	http    *http.Client
	sleep   func(time.Duration)
}

func NewInfraiClient(baseURL, key string, httpClient *http.Client) *InfraiClient {
	return &InfraiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     key,
		http:    httpClient,
		sleep:   time.Sleep,
	}
}

func (c *InfraiClient) Upload(ctx context.Context, filename string, content []byte, requestID string) (imageData, error) {
	body, err := json.Marshal(map[string]any{
		"file":     base64.StdEncoding.EncodeToString(content),
		"filename": filename,
	})
	if err != nil {
		return imageData{}, err
	}

	return c.imageRequest(ctx, http.MethodPost, "/v1/image/upload", body, "application/json", requestID+":upload")
}

func (c *InfraiClient) SmartCrop(ctx context.Context, image, requestID string) (imageData, error) {
	body, err := json.Marshal(map[string]any{"image": image, "aspect": "1:1"})
	if err != nil {
		return imageData{}, err
	}
	return c.imageRequest(ctx, http.MethodPost, "/v1/image/smart_crop", body, "application/json", requestID+":crop")
}

func (c *InfraiClient) Optimize(ctx context.Context, image, requestID string) (imageData, error) {
	body, err := json.Marshal(map[string]any{
		"image": image, "width": 512, "height": 512, "fit": "cover",
		"enlarge": false, "format": "webp", "store": true,
	})
	if err != nil {
		return imageData{}, err
	}
	return c.imageRequest(ctx, http.MethodPost, "/v1/image/resize", body, "application/json", requestID+":optimize")
}

func (c *InfraiClient) UpdateUserAvatar(ctx context.Context, userID string, avatar imageData, requestID string) error {
	body, err := json.Marshal(map[string]any{
		"user_id":  userID,
		"metadata": map[string]any{"avatar_image_id": avatar.ID, "avatar_url": avatar.URL},
	})
	if err != nil {
		return err
	}
	_, err = c.request(ctx, http.MethodPatch, "/v1/auth/user/update/"+url.PathEscape(userID), body, "application/json", requestID+":user")
	return err
}

func (c *InfraiClient) imageRequest(ctx context.Context, method, path string, body []byte, contentType, requestID string) (imageData, error) {
	env, err := c.request(ctx, method, path, body, contentType, requestID)
	if err != nil {
		return imageData{}, err
	}
	var image imageData
	if err := json.Unmarshal(env.Data, &image); err != nil {
		return imageData{}, fmt.Errorf("decode image data: %w", err)
	}
	if image.ID == "" && image.URL == "" {
		return imageData{}, errors.New("image response has no reference")
	}
	if image.ID != "" {
		image.URL = firstNonEmpty(image.URL, image.ID)
	}
	return image, nil
}

func (c *InfraiClient) request(ctx context.Context, method, path string, body []byte, contentType, requestID string) (envelope, error) {
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return envelope{}, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Idempotency-Key", requestID)

		res, err := c.http.Do(req)
		if err != nil {
			return envelope{}, fmt.Errorf("send Infrai request: %w", err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		res.Body.Close()
		if readErr != nil {
			return envelope{}, fmt.Errorf("read Infrai response: %w", readErr)
		}

		var env envelope
		decodeErr := json.Unmarshal(payload, &env)
		if decodeErr == nil && !env.OK {
			if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
				c.sleep(retryDelay(res.Header.Get("Retry-After"), attempt))
				continue
			}
			if res.StatusCode >= 500 {
				return envelope{}, fmt.Errorf("upstream response status %d", res.StatusCode)
			}
			apiErr := &APIError{HTTPStatus: res.StatusCode, Message: "request rejected"}
			if env.Error != nil {
				apiErr.Code = env.Error.Code
				apiErr.Message = env.Error.Message
			}
			return envelope{}, apiErr
		}
		if decodeErr != nil {
			return envelope{}, fmt.Errorf("decode Infrai envelope: %w", decodeErr)
		}
		if res.StatusCode >= 500 {
			return envelope{}, fmt.Errorf("upstream response status %d", res.StatusCode)
		}
		return env, nil
	}
	return envelope{}, errors.New("retry limit reached")
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
