package upstreams

import (
	"artifacts-proxy/pkg/util"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/errors"
)

type Package struct {
	Versions map[string]Version         `json:"versions"`
	Rest     map[string]json.RawMessage `json:"-"`
}

type Version struct {
	Dist Dist                       `json:"dist"`
	Rest map[string]json.RawMessage `json:"-"`
}

type Dist struct {
	Tarball string                     `json:"tarball"`
	Rest    map[string]json.RawMessage `json:"-"`
}

func (p *Package) UnmarshalJSON(data []byte) (err error) {
	p.Rest, err = util.UnmarshalWithRest(data, map[string]any{"versions": &p.Versions})
	return
}
func (p Package) MarshalJSON() ([]byte, error) {
	return util.MarshalWithRest(map[string]any{"versions": p.Versions}, p.Rest)
}

func (v *Version) UnmarshalJSON(data []byte) (err error) {
	v.Rest, err = util.UnmarshalWithRest(data, map[string]any{"dist": &v.Dist})
	return
}
func (v Version) MarshalJSON() ([]byte, error) {
	return util.MarshalWithRest(map[string]any{"dist": v.Dist}, v.Rest)
}

func (d *Dist) UnmarshalJSON(data []byte) (err error) {
	d.Rest, err = util.UnmarshalWithRest(data, map[string]any{"tarball": &d.Tarball})
	return
}
func (d Dist) MarshalJSON() ([]byte, error) {
	return util.MarshalWithRest(map[string]any{"tarball": d.Tarball}, d.Rest)
}

type NpmUpstream struct {
	publicOrigin string
	path         string
	upstreamUrl  string
}

func NewNpmUpstream(publicOrigin, path, upstreamUrl string) NpmUpstream {
	return NpmUpstream{
		publicOrigin: publicOrigin,
		path:         path,
		upstreamUrl:  upstreamUrl,
	}
}

func (u NpmUpstream) Type() string {
	return "npm"
}

func (u NpmUpstream) ModifyRequest(req *http.Request) error {
	return nil
}

func (u NpmUpstream) replaceTarballHost(requestUrl string, data []byte) ([]byte, error) {
	var pkg Package

	// Gracefully handle, since we only know to handle package data JSON.
	if err := json.Unmarshal(data, &pkg); err != nil {
		slog.Warn("failed to unmarshal npm package data", slog.String("err", err.Error()), slog.String("url", requestUrl))
		return data, nil
	}

	for key, v := range pkg.Versions {
		if relative, found := strings.CutPrefix(v.Dist.Tarball, u.upstreamUrl); found {
			tarball, err := url.JoinPath(u.publicOrigin, u.path, relative)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to join paths: %s - %s - %s", u.publicOrigin, u.path, relative)
			}
			v.Dist.Tarball = tarball
			pkg.Versions[key] = v
		}
	}

	out, err := json.Marshal(pkg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal npm package data")
	}
	return out, nil
}

func (u NpmUpstream) ModifyResponse(resp *http.Response) (io.ReadCloser, error) {
	if resp.Header.Get("Content-Type") == "application/json" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read response body")
		}

		newBody, err := u.replaceTarballHost(resp.Request.RequestURI, body)
		if err != nil {
			return nil, errors.Wrap(err, "failed to replace tarball host for package")
		}

		return io.NopCloser(bytes.NewReader(newBody)), nil
	}

	return resp.Body, nil
}
