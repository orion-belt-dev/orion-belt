package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// In-cluster defaults: the service account Kubernetes mounts into every pod.
const (
	DefaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	DefaultCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// Config locates the API server and the agent's credentials. Empty fields
// take the in-cluster defaults.
type Config struct {
	APIServer string // https://host:port; default from KUBERNETES_SERVICE_HOST/PORT
	TokenFile string
	CAFile    string
}

// Client is the agent's API server client: RBAC bindings and the proxy
// transport. It needs no client-go; the RBAC API is a handful of REST calls.
type Client struct {
	base      *url.URL
	tokenFile string
	http      *http.Client
	// Transport is shared with the kubectl proxy.
	Transport *http.Transport
}

// NewClient builds a client from cfg, falling back to the in-cluster
// service account.
func NewClient(cfg Config) (*Client, error) {
	server := cfg.APIServer
	if server == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, errors.New("kubernetes: not running in a cluster (KUBERNETES_SERVICE_HOST unset) and no api_server configured")
		}
		server = "https://" + net.JoinHostPort(host, port)
	}
	base, err := url.Parse(server)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, fmt.Errorf("kubernetes: api_server %q must be an https URL", server)
	}
	tokenFile := cfg.TokenFile
	if tokenFile == "" {
		tokenFile = DefaultTokenFile
	}
	caFile := cfg.CAFile
	if caFile == "" {
		caFile = DefaultCAFile
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("kubernetes: no certificates in %s", caFile)
	}
	if _, err := os.Stat(tokenFile); err != nil {
		return nil, fmt.Errorf("kubernetes: service account token: %w", err)
	}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		Proxy:               nil, // never route cluster credentials through an environment proxy
		ForceAttemptHTTP2:   false,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Client{
		base:      base,
		tokenFile: tokenFile,
		http:      &http.Client{Transport: tr, Timeout: 30 * time.Second},
		Transport: tr,
	}, nil
}

// Base is the API server URL.
func (c *Client) Base() *url.URL { return c.base }

// Token reads the service account token. Kubernetes rotates projected
// tokens, so it is read per request rather than cached.
func (c *Client) Token() (string, error) {
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("kubernetes: read token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

const rbacPath = "/apis/rbac.authorization.k8s.io/v1"

// object is the subset of a (Cluster)RoleBinding the agent reads and writes.
type object struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   metadata `json:"metadata"`
	RoleRef    roleRef  `json:"roleRef"`
	Subjects   []struct {
		Kind     string `json:"kind"`
		APIGroup string `json:"apiGroup,omitempty"`
		Name     string `json:"name"`
	} `json:"subjects"`
}

type metadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type roleRef struct {
	APIGroup string `json:"apiGroup"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

func toObject(b Binding) object {
	o := object{
		APIVersion: "rbac.authorization.k8s.io/v1",
		Kind:       b.Kind(),
		Metadata: metadata{
			Name:      b.Name,
			Namespace: b.Namespace,
			Labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelPermission: b.Permission,
			},
			Annotations: map[string]string{
				AnnotationUser:  b.User,
				AnnotationScope: b.Scope().String(),
			},
		},
		RoleRef: roleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: b.ClusterRole},
	}
	if b.ExpiresAt != nil {
		o.Metadata.Annotations[AnnotationExpiresAt] = b.ExpiresAt.UTC().Format(time.RFC3339)
	}
	o.Subjects = append(o.Subjects, struct {
		Kind     string `json:"kind"`
		APIGroup string `json:"apiGroup,omitempty"`
		Name     string `json:"name"`
	}{Kind: "User", APIGroup: "rbac.authorization.k8s.io", Name: b.Subject})
	return o
}

func fromObject(o object) Binding {
	b := Binding{
		Name:        o.Metadata.Name,
		Namespace:   o.Metadata.Namespace,
		ClusterRole: o.RoleRef.Name,
		User:        o.Metadata.Annotations[AnnotationUser],
		Permission:  o.Metadata.Labels[LabelPermission],
	}
	if o.Kind == "ClusterRoleBinding" {
		b.Namespace = ""
	}
	for _, s := range o.Subjects {
		if s.Kind == "User" {
			b.Subject = s.Name
			break
		}
	}
	if raw := o.Metadata.Annotations[AnnotationExpiresAt]; raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			b.ExpiresAt = &t
		}
	}
	return b
}

func collectionPath(b Binding) string {
	if b.Namespace == "" {
		return rbacPath + "/clusterrolebindings"
	}
	return rbacPath + "/namespaces/" + url.PathEscape(b.Namespace) + "/rolebindings"
}

// List returns every binding Orion Belt manages in the cluster, both kinds.
func (c *Client) List(ctx context.Context) ([]Binding, error) {
	selector := url.Values{"labelSelector": {LabelManagedBy + "=" + ManagedByValue}}.Encode()
	var out []Binding
	for _, path := range []string{rbacPath + "/rolebindings", rbacPath + "/clusterrolebindings"} {
		var list struct {
			Items []object `json:"items"`
		}
		if err := c.do(ctx, http.MethodGet, path+"?"+selector, nil, &list); err != nil {
			return nil, err
		}
		for _, o := range list.Items {
			out = append(out, fromObject(o))
		}
	}
	return out, nil
}

// Apply creates b, or replaces it when it already exists.
func (c *Client) Apply(ctx context.Context, b Binding) error {
	if err := ValidateName(b); err != nil {
		return err
	}
	// Only gateway users are ever bound; never a service account or group.
	if !strings.HasPrefix(b.Subject, SubjectPrefix) || len(b.Subject) == len(SubjectPrefix) {
		return fmt.Errorf("kubernetes: subject %q is not an Orion Belt user", b.Subject)
	}
	if !roleRE.MatchString(b.ClusterRole) {
		return fmt.Errorf("kubernetes: invalid ClusterRole %q", b.ClusterRole)
	}
	o := toObject(b)
	err := c.do(ctx, http.MethodPost, collectionPath(b), o, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusConflict {
		return c.do(ctx, http.MethodPut, collectionPath(b)+"/"+url.PathEscape(b.Name), o, nil)
	}
	return err
}

// Delete removes b. A binding already gone is not an error.
func (c *Client) Delete(ctx context.Context, b Binding) error {
	if err := ValidateName(b); err != nil {
		return err
	}
	err := c.do(ctx, http.MethodDelete, collectionPath(b)+"/"+url.PathEscape(b.Name), nil, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// ValidateName refuses to touch anything but an Orion Belt binding, so a
// gateway (or a bug) cannot use the agent to edit other RBAC objects.
func ValidateName(b Binding) error {
	if !strings.HasPrefix(b.Name, "orion-") || strings.ContainsAny(b.Name, "/?#") {
		return fmt.Errorf("kubernetes: %q is not an Orion Belt binding", b.Name)
	}
	if b.Namespace != "" && !namespaceRE.MatchString(b.Namespace) {
		return fmt.Errorf("kubernetes: invalid namespace %q", b.Namespace)
	}
	return nil
}

// StatusError is a non-2xx API server answer.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("kubernetes: %d %s", e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return err
	}
	token, err := c.Token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kubernetes: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		var status struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &status)
		if status.Message == "" {
			status.Message = http.StatusText(resp.StatusCode)
		}
		return &StatusError{Code: resp.StatusCode, Message: status.Message}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
