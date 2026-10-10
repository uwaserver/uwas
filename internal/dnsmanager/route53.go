package dnsmanager

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"sort"
	"strings"
	"time"
)

// Route53Provider implements DNS management via AWS Route53 API.
type Route53Provider struct {
	accessKey string
	secretKey string
	region    string
	client    *http.Client
	baseURL   string
}

// NewRoute53 creates a Route53 DNS provider.
func NewRoute53(accessKey, secretKey, region string) *Route53Provider {
	if region == "" {
		region = "us-east-1"
	}
	return &Route53Provider{
		accessKey: accessKey,
		secretKey: secretKey,
		region:    region,
		client:    &http.Client{Timeout: 30 * time.Second},
		baseURL:   r53BaseURL,
	}
}

const r53BaseURL = "https://route53.amazonaws.com/2013-04-01"

func (p *Route53Provider) ListZones() ([]Zone, error) {
	var zones []Zone
	marker := ""
	// Follow IsTruncated/NextMarker; without this only the first page
	// (100 zones) was returned.
	for page := 0; page < 1000; page++ { // hard cap: runaway guard
		path := "/hostedzone"
		if marker != "" {
			path += "?marker=" + neturl.QueryEscape(marker)
		}
		body, err := p.r53Request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			HostedZones []struct {
				Id   string `xml:"Id"`
				Name string `xml:"Name"`
			} `xml:"HostedZones>HostedZone"`
			IsTruncated bool   `xml:"IsTruncated"`
			NextMarker  string `xml:"NextMarker"`
		}
		if err := xml.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		for _, hz := range resp.HostedZones {
			id := strings.TrimPrefix(hz.Id, "/hostedzone/")
			zones = append(zones, Zone{ID: id, Name: strings.TrimSuffix(hz.Name, "."), Status: "active"})
		}
		if !resp.IsTruncated || resp.NextMarker == "" {
			return zones, nil
		}
		marker = resp.NextMarker
	}
	return nil, fmt.Errorf("route53: zone pagination exceeds 1000 pages")
}

func (p *Route53Provider) FindZoneByDomain(domain string) (*Zone, error) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	zones, err := p.ListZones()
	if err != nil {
		return nil, err
	}
	// Route53 returns zones in creation order, but we need the most-specific
	// zone (longest name) to match first. Sort longest-first so that a
	// subdomain-of-subdomain (e.g. www.foo.example.com) resolves to the
	// correct zone (foo.example.com) even when the parent zone (example.com)
	// was created earlier and appears first in the API response.
	sort.Slice(zones, func(i, j int) bool {
		return len(zones[i].Name) > len(zones[j].Name)
	})
	for _, z := range zones {
		name := strings.ToLower(strings.TrimSuffix(z.Name, "."))
		if name == domain || strings.HasSuffix(domain, "."+name) {
			return &z, nil
		}
	}
	return nil, fmt.Errorf("zone not found for domain %s", domain)
}

// r53RRSet is one Route53 resource record set exactly as stored: Route53's
// unit of change is the set (name+type, one TTL, every value), and Values are
// the raw wire values (TXT/SPF still quoted), which DELETE must echo verbatim.
type r53RRSet struct {
	Name   string
	Type   string
	TTL    int
	Values []string
}

// ListRecords flattens each record set into one Record per value. A value's
// ID is name:type:base64url(raw value) so Update/Delete can address that one
// value instead of the whole set; TXT/SPF content is returned unquoted, the
// same presentation form every other provider returns.
func (p *Route53Provider) ListRecords(zoneID string) ([]Record, error) {
	sets, err := p.listRRSets(zoneID)
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, set := range sets {
		for _, raw := range set.Values {
			records = append(records, Record{
				ID:      r53RecordID(set.Name, set.Type, raw),
				Type:    set.Type,
				Name:    strings.TrimSuffix(set.Name, "."),
				Content: r53DecodeValue(set.Type, raw),
				TTL:     set.TTL,
			})
		}
	}
	return records, nil
}

func (p *Route53Provider) listRRSets(zoneID string) ([]r53RRSet, error) {
	var sets []r53RRSet
	nextName, nextType := "", ""
	// Follow IsTruncated/NextRecordName+NextRecordType; without this only the
	// first page (100 record sets) was returned.
	for page := 0; page < 1000; page++ { // hard cap: runaway guard
		path := "/hostedzone/" + zoneID + "/rrset"
		if nextName != "" {
			// Keep query params alphabetically ordered (name before type) so
			// the SigV4 canonical request matches what AWS computes.
			path += "?name=" + neturl.QueryEscape(nextName)
			if nextType != "" {
				path += "&type=" + neturl.QueryEscape(nextType)
			}
		}
		body, err := p.r53Request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			ResourceRecordSets []struct {
				Name string `xml:"Name"`
				Type string `xml:"Type"`
				TTL  int    `xml:"TTL"`
				Recs []struct {
					Value string `xml:"Value"`
				} `xml:"ResourceRecords>ResourceRecord"`
			} `xml:"ResourceRecordSets>ResourceRecordSet"`
			IsTruncated    bool   `xml:"IsTruncated"`
			NextRecordName string `xml:"NextRecordName"`
			NextRecordType string `xml:"NextRecordType"`
		}
		if err := xml.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		for _, rrs := range resp.ResourceRecordSets {
			set := r53RRSet{Name: rrs.Name, Type: rrs.Type, TTL: rrs.TTL}
			for _, rr := range rrs.Recs {
				set.Values = append(set.Values, rr.Value)
			}
			sets = append(sets, set)
		}
		if !resp.IsTruncated || resp.NextRecordName == "" {
			return sets, nil
		}
		nextName, nextType = resp.NextRecordName, resp.NextRecordType
	}
	return nil, fmt.Errorf("route53: record pagination exceeds 1000 pages")
}

// CreateRecord adds one value. Route53's CREATE fails when the set already
// exists; the set is then extended with UPSERT, keeping its TTL and every
// sibling value.
func (p *Route53Provider) CreateRecord(zoneID string, rec Record) (*Record, error) {
	name := r53FQDN(rec.Name)
	raw := r53EncodeValue(rec.Type, rec.Content)
	rec.ID = r53RecordID(name, rec.Type, raw)
	err := p.submitChanges(zoneID, r53Change{"CREATE", name, rec.Type, r53DefaultTTL(rec.TTL), []string{raw}})
	var apiErr *r53APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.status != http.StatusBadRequest || !strings.Contains(apiErr.body, "already exists") {
		if err != nil {
			return nil, err
		}
		return &rec, nil
	}
	sets, err := p.listRRSets(zoneID)
	if err != nil {
		return nil, err
	}
	set := r53FindSet(sets, name, rec.Type)
	if set == nil {
		return nil, apiErr
	}
	if !r53HasValue(set.Values, raw) {
		if err := p.submitChanges(zoneID, r53Change{"UPSERT", set.Name, set.Type, set.TTL, r53WithValue(set.Values, raw)}); err != nil {
			return nil, err
		}
	}
	return &rec, nil
}

// r53APIError is an HTTP error response from Route53; Error() keeps the
// message format callers already log.
type r53APIError struct {
	method, path string
	status       int
	body         string
}

func (e *r53APIError) Error() string {
	return fmt.Sprintf("Route53 %s %s: %d — %s", e.method, e.path, e.status, e.body)
}

// UpdateRecord replaces the one value named by recordID, leaving the set's
// other values in place. A legacy name:type ID (no value) cannot say which
// value is meant and keeps the old whole-set UPSERT.
func (p *Route53Provider) UpdateRecord(zoneID, recordID string, rec Record) (*Record, error) {
	idName, idType, oldRaw, hasValue, err := parseR53RecordID(recordID)
	if err != nil {
		return nil, err
	}
	name := r53FQDN(rec.Name)
	newRaw := r53EncodeValue(rec.Type, rec.Content)
	if !hasValue {
		if err := p.submitChanges(zoneID, r53Change{"UPSERT", name, rec.Type, r53DefaultTTL(rec.TTL), []string{newRaw}}); err != nil {
			return nil, err
		}
		rec.ID = r53RecordID(name, rec.Type, newRaw)
		return &rec, nil
	}
	sets, err := p.listRRSets(zoneID)
	if err != nil {
		return nil, err
	}
	old := r53FindSet(sets, idName, idType)
	if old == nil || !r53HasValue(old.Values, oldRaw) {
		return nil, fmt.Errorf("record not found: %s", recordID)
	}
	var changes []r53Change
	if target := r53FindSet(sets, name, rec.Type); target == old {
		values := r53WithValue(r53WithoutValue(old.Values, oldRaw), newRaw)
		ttl := old.TTL
		if rec.TTL > 0 {
			ttl = rec.TTL
		}
		changes = append(changes, r53Change{"UPSERT", old.Name, old.Type, ttl, values})
	} else {
		// Name or type changed: move the value in one atomic batch.
		changes = append(changes, r53RemoveValue(old, oldRaw))
		if target == nil {
			changes = append(changes, r53Change{"CREATE", name, rec.Type, r53DefaultTTL(rec.TTL), []string{newRaw}})
		} else if !r53HasValue(target.Values, newRaw) {
			changes = append(changes, r53Change{"UPSERT", target.Name, target.Type, target.TTL, r53WithValue(target.Values, newRaw)})
		}
	}
	if err := p.submitChanges(zoneID, changes...); err != nil {
		return nil, err
	}
	rec.ID = r53RecordID(name, rec.Type, newRaw)
	return &rec, nil
}

// DeleteRecord removes the one value named by recordID; the set survives with
// its other values. A legacy name:type ID deletes the whole set, as before.
func (p *Route53Provider) DeleteRecord(zoneID, recordID string) error {
	name, typ, raw, hasValue, err := parseR53RecordID(recordID)
	if err != nil {
		return err
	}
	sets, err := p.listRRSets(zoneID)
	if err != nil {
		return err
	}
	set := r53FindSet(sets, name, typ)
	if set == nil || (hasValue && !r53HasValue(set.Values, raw)) {
		return fmt.Errorf("record not found: %s", recordID)
	}
	if !hasValue {
		// Route53 requires DELETE to match the existing TTL and every value.
		return p.submitChanges(zoneID, r53Change{"DELETE", set.Name, set.Type, set.TTL, set.Values})
	}
	return p.submitChanges(zoneID, r53RemoveValue(set, raw))
}

type r53Change struct {
	Action string
	Name   string
	Type   string
	TTL    int
	Values []string
}

// r53RemoveValue drops raw from set: an UPSERT of the remaining values, or a
// DELETE (echoing the current TTL and values) when raw was the last one.
func r53RemoveValue(set *r53RRSet, raw string) r53Change {
	rest := r53WithoutValue(set.Values, raw)
	if len(rest) == 0 {
		return r53Change{"DELETE", set.Name, set.Type, set.TTL, set.Values}
	}
	return r53Change{"UPSERT", set.Name, set.Type, set.TTL, rest}
}

func (p *Route53Provider) submitChanges(zoneID string, changes ...r53Change) error {
	var body strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&body, `
      <Change>
        <Action>%s</Action>
        <ResourceRecordSet>
          <Name>%s</Name>
          <Type>%s</Type>
          <TTL>%d</TTL>
          <ResourceRecords>
            `, c.Action, xmlEscape(c.Name), xmlEscape(c.Type), c.TTL)
		for _, value := range c.Values {
			body.WriteString("<ResourceRecord><Value>" + xmlEscape(value) + "</Value></ResourceRecord>")
		}
		body.WriteString(`
          </ResourceRecords>
        </ResourceRecordSet>
      </Change>`)
	}
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeBatch>
    <Changes>` + body.String() + `
    </Changes>
  </ChangeBatch>
</ChangeResourceRecordSetsRequest>`
	_, err := p.r53Request("POST", "/hostedzone/"+zoneID+"/rrset", []byte(xmlBody))
	return err
}

func r53FQDN(name string) string {
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}

func r53DefaultTTL(ttl int) int {
	if ttl == 0 {
		return 300
	}
	return ttl
}

// r53SameName normalises a record name the way Route53 stores it: lower case,
// no trailing dot, and "*" spelled \052 on the wire. A caller's "*.example.com"
// or "WWW.example.com" must find the set listed as \052.example.com. / www.
func r53SameName(name string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	return strings.ReplaceAll(name, `\052`, "*")
}

func r53FindSet(sets []r53RRSet, name, typ string) *r53RRSet {
	name = r53SameName(name)
	for i := range sets {
		if r53SameName(sets[i].Name) == name && sets[i].Type == typ {
			return &sets[i]
		}
	}
	return nil
}

func r53HasValue(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func r53WithoutValue(values []string, v string) []string {
	out := make([]string, 0, len(values))
	for _, x := range values {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func r53WithValue(values []string, v string) []string {
	if r53HasValue(values, v) {
		return append([]string(nil), values...)
	}
	return append(append([]string(nil), values...), v)
}

func r53RecordID(name, typ, raw string) string {
	return name + ":" + typ + ":" + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// parseR53RecordID accepts name:type:base64url(value) and the legacy
// name:type form (hasValue false). DNS names and types never contain ':'.
func parseR53RecordID(id string) (name, typ, raw string, hasValue bool, err error) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) < 2 {
		return "", "", "", false, fmt.Errorf("invalid record ID: %s", id)
	}
	if len(parts) == 2 {
		return parts[0], parts[1], "", false, nil
	}
	v, decErr := base64.RawURLEncoding.DecodeString(parts[2])
	if decErr != nil {
		return "", "", "", false, fmt.Errorf("invalid record ID: %s", id)
	}
	return parts[0], parts[1], string(v), true, nil
}

func r53QuotedType(typ string) bool {
	typ = strings.ToUpper(typ)
	return typ == "TXT" || typ == "SPF"
}

// r53EncodeValue turns a TXT/SPF value into Route53's required wire form:
// one or more double-quoted strings of at most 255 bytes, with '"' and '\'
// escaped. A value that is already quoted is sent as given; other types are
// unchanged.
func r53EncodeValue(typ, v string) string {
	if !r53QuotedType(typ) || (len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"') {
		return v
	}
	var b strings.Builder
	for first := true; first || len(v) > 0; first = false {
		n := len(v)
		if n > 255 {
			n = 255
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		for i := 0; i < n; i++ {
			if v[i] == '"' || v[i] == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(v[i])
		}
		b.WriteByte('"')
		v = v[n:]
	}
	return b.String()
}

// r53DecodeValue turns a quoted TXT/SPF wire value back into its text: the
// quoted strings are concatenated, \X becomes X and \DDD the octal byte.
// Anything it cannot parse is returned unchanged.
func r53DecodeValue(typ, raw string) string {
	if !r53QuotedType(typ) || !strings.HasPrefix(raw, `"`) {
		return raw
	}
	var out []byte
	i := 0
	for i < len(raw) {
		if raw[i] == ' ' || raw[i] == '\t' {
			i++
			continue
		}
		if raw[i] != '"' {
			return raw
		}
		i++
		closed := false
		for i < len(raw) {
			c := raw[i]
			if c == '"' {
				closed = true
				i++
				break
			}
			if c == '\\' {
				if i+3 < len(raw) && isOctal(raw[i+1]) && isOctal(raw[i+2]) && isOctal(raw[i+3]) {
					n := int(raw[i+1]-'0')*64 + int(raw[i+2]-'0')*8 + int(raw[i+3]-'0')
					if n > 255 {
						return raw
					}
					out = append(out, byte(n))
					i += 4
					continue
				}
				if i+1 >= len(raw) {
					return raw
				}
				out = append(out, raw[i+1])
				i += 2
				continue
			}
			out = append(out, c)
			i++
		}
		if !closed {
			return raw
		}
	}
	return string(out)
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// r53Request makes a signed AWS request to Route53.
func (p *Route53Provider) r53Request(method, path string, body []byte) ([]byte, error) {
	url := p.baseURL + path
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	// AWS Signature Version 4
	now := time.Now().UTC()
	req.Header.Set("Host", "route53.amazonaws.com")
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	if body != nil {
		req.Header.Set("Content-Type", "text/xml")
	}
	p.signRequest(req, body, now)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("route53: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &r53APIError{method: method, path: path, status: resp.StatusCode, body: string(data)}
	}
	return data, nil
}

func (p *Route53Provider) signRequest(req *http.Request, body []byte, t time.Time) {
	// Simplified AWS Sig V4 for Route53
	dateStamp := t.Format("20060102")
	amzDate := t.Format("20060102T150405Z")
	service := "route53"

	// Route53 is a global service reached at route53.amazonaws.com and MUST be
	// signed against us-east-1 regardless of the account's configured region;
	// using p.region here produced SignatureDoesNotMatch for any other region.
	const signingRegion = "us-east-1"

	// Canonical request
	bodyHash := sha256hex(body)
	headers := []string{"host", "x-amz-date"}
	sort.Strings(headers)
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-date:%s\n", "route53.amazonaws.com", amzDate)
	signedHeaders := strings.Join(headers, ";")
	canonicalReq := strings.Join([]string{
		req.Method, req.URL.Path, req.URL.RawQuery,
		canonicalHeaders, signedHeaders, bodyHash,
	}, "\n")

	// String to sign
	credScope := dateStamp + "/" + signingRegion + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credScope + "\n" + sha256hex([]byte(canonicalReq))

	// Signing key
	kDate := hmacSHA256([]byte("AWS4"+p.secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(signingRegion))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	sig := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.accessKey, credScope, signedHeaders, sig))
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// xmlEscape escapes a string for safe inclusion in XML text content.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
