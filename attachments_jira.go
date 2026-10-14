package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

func (c *jiraClient) attachFile(ctx context.Context, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return err
	}
	n, err := io.Copy(part, io.LimitReader(f, (25<<20)+1))
	if err != nil {
		return err
	}
	if n > 25<<20 {
		return fmt.Errorf("%s exceeds 25 MiB", filepath.Base(path))
	}
	if err := writer.Close(); err != nil {
		return err
	}
	cr, err := c.creds()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", cr.Site+"/rest/api/3/issue/"+url.PathEscape(key)+"/attachments", &b)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cr.Email, cr.Token)
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("uploading %s: Jira HTTP %d", filepath.Base(path), res.StatusCode)
	}
	return nil
}
