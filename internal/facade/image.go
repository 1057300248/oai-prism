package facade

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oai-prism/oaiprism/internal/prism"
)

var httpClientForImage = &http.Client{
	Timeout: 15 * time.Second,
}

// preprocessInputImages 将输入中客户端传入的 base64 或外部图片转存为项目文件，使得上游 Codex 能够正常读取和分析。
func preprocessInputImages(ctx context.Context, client *prism.Client, p prism.Principal, projectID string, items []prism.InputItem) []prism.InputItem {
	if projectID == "" || len(items) == 0 {
		return items
	}

	outItems := make([]prism.InputItem, len(items))
	for i, item := range items {
		outItems[i] = item
		hasImage := false
		for _, c := range item.Content {
			if c.Type == "input_image" && c.ImageURL != "" {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}

		newContents := make([]prism.InputContent, 0, len(item.Content)+1)
		for _, c := range item.Content {
			if c.Type != "input_image" || c.ImageURL == "" {
				newContents = append(newContents, c)
				continue
			}

			// 判断是否是 base64 或外部 HTTP
			data, ext, ok := extractImageData(ctx, c.ImageURL)
			if !ok || len(data) == 0 {
				newContents = append(newContents, c)
				continue
			}

			filename := "image_" + randHex(6) + ext
			// 代上传到项目存储
			_, err := client.UploadFile(ctx, p, prism.FileUpload{
				ProjectID: projectID,
				Path:      filename,
				Filename:  filename,
				Data:      data,
			})
			if err != nil {
				// 上传失败时保留原样
				newContents = append(newContents, c)
				continue
			}

			// 上传成功：转换为上游原生识别的 input_file
			newContents = append(newContents, prism.InputContent{
				Type:        "input_file",
				Filename:    filename,
				ProjectPath: filename,
			})
		}
		outItems[i].Content = newContents
	}
	return outItems
}

func extractImageData(ctx context.Context, imgURL string) ([]byte, string, bool) {
	// 1. Base64 Data URI
	if strings.HasPrefix(imgURL, "data:image/") {
		comma := strings.IndexByte(imgURL, ',')
		if comma == -1 {
			return nil, "", false
		}
		header := imgURL[:comma]
		b64Data := imgURL[comma+1:]

		ext := ".png"
		if strings.Contains(header, "image/jpeg") || strings.Contains(header, "image/jpg") {
			ext = ".jpg"
		} else if strings.Contains(header, "image/webp") {
			ext = ".webp"
		} else if strings.Contains(header, "image/gif") {
			ext = ".gif"
		}

		decoded, err := base64.StdEncoding.DecodeString(b64Data)
		if err != nil {
			return nil, "", false
		}
		return decoded, ext, true
	}

	// 2. 外部 HTTP/HTTPS 链接（排除 prism 内部域名）
	if (strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://")) && !strings.Contains(imgURL, "prism.openai.com") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
		if err != nil {
			return nil, "", false
		}
		resp, err := httpClientForImage.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			return nil, "", false
		}
		defer func() { _ = resp.Body.Close() }()

		data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 限制 10MB
		if err != nil {
			return nil, "", false
		}

		ext := ".png"
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg") {
			ext = ".jpg"
		} else if strings.Contains(ct, "webp") {
			ext = ".webp"
		}
		return data, ext, true
	}

	return nil, "", false
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
