package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

type Params struct {
	UpstreamName string `json:"upstream_name"`
	URL          string `json:"url"`
	Method       string `json:"method"`
}

func (p Params) Hash() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

type Metadata struct {
	Params      Params  `json:"params"`
	ContentType *string `json:"content_type"`
	StatusCode  int     `json:"status_code"`
	LastUpdated string  `json:"last_updated"`
}
