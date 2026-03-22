package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// S3Provider uploads backups to an S3-compatible object store using raw HTTP
// with AWS Signature V4 authentication. No AWS SDK dependency is required.
type S3Provider struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
	region    string
	client    *http.Client
}

// NewS3Provider creates an S3Provider.
func NewS3Provider(endpoint, bucket, accessKey, secretKey, region string) *S3Provider {
	if region == "" {
		region = "us-east-1"
	}
	return &S3Provider{
		endpoint:  endpoint,
		bucket:    bucket,
		accessKey: accessKey,
		secretKey: secretKey,
		region:    region,
		client:    &http.Client{Timeout: 5 * time.Minute},
	}
}

func (p *S3Provider) Name() string { return "s3" }

func (p *S3Provider) Upload(ctx context.Context, filename string, data io.Reader) error {
	url := p.objectURL(filename)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, data)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/gzip")
	p.signRequest(req, "UNSIGNED-PAYLOAD")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("s3 upload %s: %s %s", filename, resp.Status, string(body))
	}
	return nil
}

func (p *S3Provider) Download(ctx context.Context, filename string) (io.ReadCloser, error) {
	url := p.objectURL(filename)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	p.signRequest(req, "UNSIGNED-PAYLOAD")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("s3 download %s: %s", filename, resp.Status)
	}
	return resp.Body, nil
}

// listBucketResult is the XML response from S3 ListObjectsV2.
type listBucketResult struct {
	XMLName  xml.Name       `xml:"ListBucketResult"`
	Contents []s3ObjectInfo `xml:"Contents"`
}

type s3ObjectInfo struct {
	Key          string `xml:"Key"`
	Size         int64  `xml:"Size"`
	LastModified string `xml:"LastModified"`
}

func (p *S3Provider) List(ctx context.Context) ([]BackupInfo, error) {
	url := p.bucketURL() + "?list-type=2&prefix=uwas-backup-"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	p.signRequest(req, "UNSIGNED-PAYLOAD")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("s3 list: %s %s", resp.Status, string(body))
	}

	var result listBucketResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse s3 list: %w", err)
	}

	var infos []BackupInfo
	for _, obj := range result.Contents {
		if !strings.HasSuffix(obj.Key, ".tar.gz") {
			continue
		}
		t, _ := time.Parse(time.RFC3339, obj.LastModified)
		infos = append(infos, BackupInfo{
			Name:     obj.Key,
			Size:     obj.Size,
			Created:  t,
			Provider: "s3",
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Created.After(infos[j].Created)
	})
	return infos, nil
}

func (p *S3Provider) Delete(ctx context.Context, filename string) error {
	url := p.objectURL(filename)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	p.signRequest(req, "UNSIGNED-PAYLOAD")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("s3 delete %s: %s %s", filename, resp.Status, string(body))
	}
	return nil
}

// --- URL helpers ---

func (p *S3Provider) bucketURL() string {
	scheme := "https"
	host := p.endpoint
	if host == "" {
		host = "s3." + p.region + ".amazonaws.com"
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/") + "/" + p.bucket
	}
	return scheme + "://" + host + "/" + p.bucket
}

func (p *S3Provider) objectURL(key string) string {
	return p.bucketURL() + "/" + key
}

// --- AWS Signature V4 (simplified) ---

func (p *S3Provider) signRequest(req *http.Request, payloadHash string) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}

	scope := dateStamp + "/" + p.region + "/s3/aws4_request"

	// Canonical request
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQueryString := req.URL.RawQuery
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + req.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"

	canonicalRequest := req.Method + "\n" +
		canonicalURI + "\n" +
		canonicalQueryString + "\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		payloadHash

	// String to sign
	stringToSign := "AWS4-HMAC-SHA256\n" +
		amzDate + "\n" +
		scope + "\n" +
		sha256Hex([]byte(canonicalRequest))

	// Signing key
	kDate := hmacSHA256([]byte("AWS4"+p.secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(p.region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	auth := "AWS4-HMAC-SHA256 Credential=" + p.accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + signature
	req.Header.Set("Authorization", auth)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
