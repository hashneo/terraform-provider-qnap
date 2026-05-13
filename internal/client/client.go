// Package client provides a REST/CGI API client for QNAP QTS 5.x.
//
// QNAP QTS 5 exposes two API surfaces:
//
//  1. Legacy CGI API  — POST https://<host>/cgi-bin/authLogin.cgi
//     Returns a SID (session token) used in all subsequent requests.
//     Password must be base64-encoded in the POST body.
//
//  2. File Station CGI — https://<host>/cgi-bin/filemanager/utilRequest.cgi
//     JSON responses, SID passed as query param.
//
//  3. Management CGI   — https://<host>/cgi-bin/management/manaRequest.cgi
//     XML responses, SID passed as query param. Used for system info.
//
// Note: The /api/v2/ REST endpoints are NOT available on all QTS versions/models.
// This client uses only the proven CGI endpoints for maximum compatibility.
package client

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// -----------------------------------------------------------------------
// API path constants
// -----------------------------------------------------------------------

// ErrNotFound is returned when a QTS endpoint returns HTTP 404.
var ErrNotFound = fmt.Errorf("not found")

const (
	// Auth
	pathLogin = "/cgi-bin/authLogin.cgi"

	// Legacy CGI — system info XML
	pathSysinfoCGI = "/cgi-bin/management/manaRequest.cgi"

	// File Station CGI — JSON API
	pathFileStation = "/cgi-bin/filemanager/utilRequest.cgi"
)

// -----------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------

// Client is a QNAP QTS CGI client.
type Client struct {
	Host       string
	BaseURL    string
	Username   string
	httpClient *http.Client
	sid        string
}

// New creates a new QNAP client and authenticates immediately.
func New(host, username, password string, sslInsecure bool) (*Client, error) {
	baseURL := host
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		baseURL = "https://" + host
	}
	baseURL = strings.TrimRight(baseURL, "/")

	c := &Client{
		Host:    host,
		BaseURL: baseURL,
		Username: username,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: sslInsecure}, //nolint:gosec
			},
		},
	}
	if err := c.login(username, password); err != nil {
		return nil, err
	}
	return c, nil
}

// login authenticates with QNAP's CGI auth endpoint and stores the SID.
func (c *Client) login(username, password string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(password))

	endpoint := fmt.Sprintf("%s%s", c.BaseURL, pathLogin)
	params := url.Values{}
	params.Set("user", username)
	params.Set("pwd", encoded)
	params.Set("serviceKey", "1")
	params.Set("client_id", "terraform-provider-qnap")

	resp, err := c.httpClient.PostForm(endpoint, params)
	if err != nil {
		return fmt.Errorf("qnap login POST failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("qnap login read body: %w", err)
	}

	sid := xmlField(body, "authSid")
	passed := xmlField(body, "authPassed")
	if passed != "1" || sid == "" {
		return fmt.Errorf("qnap authentication failed (authPassed=%q): check username/password", passed)
	}
	c.sid = sid
	return nil
}

// cgiGet performs a GET against a CGI endpoint with sid appended and returns the body.
func (c *Client) cgiGet(path string, query url.Values) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("sid", c.sid)
	endpoint := fmt.Sprintf("%s%s?%s", c.BaseURL, path, query.Encode())
	resp, err := c.httpClient.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("qnap GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qnap GET %s read body: %w", path, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qnap GET %s: HTTP %d", path, resp.StatusCode)
	}
	return body, nil
}

// fileStationJSON calls the File Station CGI and parses JSON into v.
func (c *Client) fileStationJSON(params url.Values, v interface{}) error {
	body, err := c.cgiGet(pathFileStation, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("qnap filemanager unmarshal: %w — body: %s", err, truncate(string(body), 200))
	}
	return nil
}

// -----------------------------------------------------------------------
// Typed API response structs
// -----------------------------------------------------------------------

// QTSResponse is the envelope that wraps every QTS REST v2 response (kept for compatibility).
type QTSResponse struct {
	Success bool            `json:"success"`
	Error   *QTSError       `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type QTSError struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

// SystemInfo maps system information parsed from the sysinfo CGI endpoint.
type SystemInfo struct {
	Hostname        string
	Model           string
	SerialNumber    string
	Firmware        string
	Build           string
	FirmwareDate    string
	Uptime          int64
	CPUModel        string
	CPUCores        int
	TotalRAMMB      int
	FreeRAMMB       int
	TemperatureC    int
	SystemTempC     int
	TimeZone        string
	NTPServer       string
	DNSPrimary      string
	DNSSecondary    string
}

// NetworkInterface represents a NIC parsed from sysinfo XML.
type NetworkInterface struct {
	Name     string
	MAC      string
	Status   string
	Speed    string
	Duplex   string
	IPv4     []string
	IPv6     []string
	Gateway  string
	MTU      int
	BondMode string
	VLANTag  int
}

// Disk represents a physical disk parsed from sysinfo XML.
type Disk struct {
	Slot     int
	Alias    string
	TempC    int
	TempF    int
	Alert    bool
	IsSSD    bool
	Installed bool
}

// SharedFolder maps a shared folder discovered via File Station API.
type SharedFolder struct {
	Name        string
	Path        string
	VolumeID    string
	Comment     string
	Owner       string
	Compression bool
	Encryption  bool
	ReadOnly    bool
	Hidden      bool
	Protocols   []string
	TotalSize   string
	ItemCount   int
}

// User maps a local user from the File Station user list API.
type User struct {
	Name        string
	UID         int
	Description string
	Email       string
	Enabled     bool
	Groups      []string
}

// Group maps a local group (populated from sysinfo or user data).
type Group struct {
	Name        string
	GID         int
	Description string
	Members     []string
}

// Volume — placeholder for storage volumes (not available via CGI on this device).
type Volume struct {
	ID          string
	Label       string
	Status      string
	FileSystem  string
	TotalBytes  int64
	UsedBytes   int64
	FreeBytes   int64
	PoolID      string
	Encrypted   bool
	Compression bool
	Dedup       bool
	Thin        bool
}

// StoragePool — placeholder for storage pools.
type StoragePool struct {
	ID         string
	Label      string
	Status     string
	RAIDType   string
	TotalBytes int64
	UsedBytes  int64
	FreeBytes  int64
	DiskCount  int
	SpareCount int
}

// ISCSITarget — placeholder for iSCSI targets.
type ISCSITarget struct {
	ID      string
	Name    string
	IQN     string
	Status  string
	Enabled bool
}

// ISCSILun — placeholder for iSCSI LUNs.
type ISCSILun struct {
	ID        string
	Name      string
	TargetID  string
	LunID     int
	SizeBytes int64
	UsedBytes int64
	Status    string
	LunType   string
	ThinProv  bool
	VolumeID  string
	FilePath  string
}

// Snapshot — placeholder for snapshots.
type Snapshot struct {
	ID          string
	Name        string
	VolumeID    string
	CreatedAt   string
	SizeBytes   int64
	Description string
	Type        string
	Status      string
}

// App maps an installed QPKG app from the File Station media folder list.
type App struct {
	Name        string
	DisplayName string
	Version     string
	Author      string
	Status      string
	Enabled     bool
	Location    string
}

// Container — placeholder.
type Container struct {
	ID          string
	Name        string
	Image       string
	Status      string
	Runtime     string
	ProjectName string
	CPUPercent  string
	MemUsage    string
	Created     string
}

// Project — placeholder.
type Project struct {
	ID     string
	Name   string
	Status string
	Path   string
}

// -----------------------------------------------------------------------
// High-level fetch helpers — implemented via CGI
// -----------------------------------------------------------------------

// SystemInfo fetches system information via the legacy CGI sysinfo endpoint.
// It returns rich data including NIC info, temps, uptime, and serial number.
func (c *Client) SystemInfo() (*SystemInfo, error) {
	q := url.Values{}
	q.Set("subfunc", "sysinfo")
	body, err := c.cgiGet(pathSysinfoCGI, q)
	if err != nil {
		return nil, fmt.Errorf("qnap sysinfo CGI: %w", err)
	}
	if xmlField(body, "authPassed") != "1" {
		return nil, fmt.Errorf("qnap sysinfo CGI: not authenticated")
	}

	info := &SystemInfo{
		Hostname:     xmlField(body, "hostname"),
		Model:        xmlField(body, "displayModelName"),
		SerialNumber: xmlField(body, "serial_number"),
		Firmware:     xmlField(body, "version"),
		Build:        xmlField(body, "build"),
		FirmwareDate: xmlField(body, "buildTime"),
		CPUModel:     xmlField(body, "cpu_model_name"),
		TimeZone:     xmlField(body, "timezone"),
		DNSPrimary:   xmlField(body, "dns1"),
	}

	// Uptime from components
	if d := xmlField(body, "uptime_day"); d != "" {
		days, _ := strconv.ParseInt(d, 10, 64)
		hours, _ := strconv.ParseInt(xmlField(body, "uptime_hour"), 10, 64)
		mins, _ := strconv.ParseInt(xmlField(body, "uptime_min"), 10, 64)
		secs, _ := strconv.ParseInt(xmlField(body, "uptime_sec"), 10, 64)
		info.Uptime = days*86400 + hours*3600 + mins*60 + secs
	}

	// Temperatures
	if v := xmlField(body, "cpu_tempc"); v != "" {
		t, _ := strconv.Atoi(v)
		info.TemperatureC = t
	}
	if v := xmlField(body, "sys_tempc"); v != "" {
		t, _ := strconv.Atoi(v)
		info.SystemTempC = t
	}

	// RAM
	if v := xmlField(body, "total_memory"); v != "" {
		var f float64
		fmt.Sscanf(v, "%f", &f)
		info.TotalRAMMB = int(f)
	}
	if v := xmlField(body, "free_memory"); v != "" {
		var f float64
		fmt.Sscanf(v, "%f", &f)
		info.FreeRAMMB = int(f)
	}

	// DNS from first NIC's dns info
	if v := xmlField(body, "dns1"); v == "" {
		info.DNSPrimary = xmlField(body, "DNS_LIST")
	}

	return info, nil
}

// NetworkInterfaces fetches NIC information from the sysinfo XML.
func (c *Client) NetworkInterfaces() ([]NetworkInterface, error) {
	q := url.Values{}
	q.Set("subfunc", "sysinfo")
	body, err := c.cgiGet(pathSysinfoCGI, q)
	if err != nil {
		return nil, err
	}

	s := string(body)
	nicCount := 0
	if v := xmlField(body, "nic_cnt"); v != "" {
		nicCount, _ = strconv.Atoi(v)
	}
	if nicCount == 0 {
		nicCount = 8 // try up to 8 if count not parsed
	}

	var ifaces []NetworkInterface
	for i := 1; i <= nicCount; i++ {
		idx := strconv.Itoa(i)
		name := xmlFieldN(s, "ifname", i)
		if name == "" {
			break
		}
		ip := xmlFieldN(s, "eth_ip", i)
		mac := xmlFieldN(s, "eth_mac", i)
		etst := xmlFieldN(s, "eth_status", i)
		speed := xmlFieldN(s, "eth_max_speed", i)
		usage := xmlFieldN(s, "eth_usage", i)
		_ = usage
		_ = idx

		st := "down"
		if etst == "1" {
			st = "up"
		}

		iface := NetworkInterface{
			Name:   name,
			MAC:    mac,
			Status: st,
			Speed:  speed + " Mbps",
		}
		if ip != "" {
			iface.IPv4 = []string{ip}
		}

		// IPv6 — look for eth_ipv6_ip1 inside the eth_ipv6_info block
		ipv6re := regexp.MustCompile(`<eth_ipv6_info` + idx + `>(.*?)</eth_ipv6_info` + idx + `>`)
		if m := ipv6re.FindStringSubmatch(s); len(m) > 1 {
			ipv6block := m[1]
			ipv6fieldRe := regexp.MustCompile(`<eth_ipv6_ip\d+><!\[CDATA\[(.*?)\]\]></eth_ipv6_ip\d+>`)
			for _, pm := range ipv6fieldRe.FindAllStringSubmatch(ipv6block, -1) {
				if len(pm) > 1 && pm[1] != "" {
					iface.IPv6 = append(iface.IPv6, pm[1])
				}
			}
		}

		// Gateway — look in routing table; for now use the IP as gateway hint
		// DNS from dnsinfo block
		dnsBlock := extractBlock(s, "dnsinfo"+idx)
		if dnsBlock != "" {
			iface.Gateway = xmlFieldFromStr(dnsBlock, "dns1")
		}

		ifaces = append(ifaces, iface)
	}
	return ifaces, nil
}

// Disks fetches disk information from the sysinfo XML.
func (c *Client) Disks() ([]Disk, error) {
	q := url.Values{}
	q.Set("subfunc", "sysinfo")
	body, err := c.cgiGet(pathSysinfoCGI, q)
	if err != nil {
		return nil, err
	}

	s := string(body)
	diskCountStr := xmlField(body, "disk_num")
	diskCount := 16
	if diskCountStr != "" {
		diskCount, _ = strconv.Atoi(diskCountStr)
	}

	var disks []Disk
	for i := 1; i <= diskCount; i++ {
		installed := xmlFieldN(s, "disk_installed", i)
		if installed == "" || installed == "0" {
			continue
		}
		tempC, _ := strconv.Atoi(xmlFieldN(s, "tempc", i))
		tempF, _ := strconv.Atoi(xmlFieldN(s, "tempf", i))
		alert := xmlFieldN(s, "temp_alert", i) == "1"
		isSSD := xmlFieldN(s, "hd_is_ssd", i) == "1"
		alias := xmlFieldN(s, "hd_pd_alias", i)

		disks = append(disks, Disk{
			Slot:      i,
			Alias:     alias,
			TempC:     tempC,
			TempF:     tempF,
			Alert:     alert,
			IsSSD:     isSSD,
			Installed: true,
		})
	}
	return disks, nil
}

// Users fetches local user accounts via the File Station user list API.
func (c *Client) Users() ([]User, error) {
	q := url.Values{}
	q.Set("func", "get_user_group_list")
	q.Set("type", "0") // 0 = local users
	q.Set("filter", "")

	var resp struct {
		Status   int    `json:"status"`
		Count    int    `json:"count"`
		UserList []struct {
			Name string `json:"name"`
		} `json:"user_list"`
	}
	if err := c.fileStationJSON(q, &resp); err != nil {
		return nil, err
	}

	users := make([]User, 0, len(resp.UserList))
	for _, u := range resp.UserList {
		users = append(users, User{
			Name:    u.Name,
			Enabled: true,
		})
	}
	return users, nil
}

// Groups fetches local groups via the File Station user list API.
func (c *Client) Groups() ([]Group, error) {
	q := url.Values{}
	q.Set("func", "get_user_group_list")
	q.Set("type", "2") // 2 = local groups

	var resp struct {
		Status    int    `json:"status"`
		Count     int    `json:"count"`
		GroupList []struct {
			Name string `json:"name"`
		} `json:"group_list"`
	}
	if err := c.fileStationJSON(q, &resp); err != nil {
		return nil, err
	}

	groups := make([]Group, 0, len(resp.GroupList))
	for _, g := range resp.GroupList {
		groups = append(groups, Group{
			Name: g.Name,
		})
	}
	return groups, nil
}

// SharedFolders discovers shared folders via the File Station media_folder_list API
// and enriches each with a file count from get_list.
func (c *Client) SharedFolders() ([]SharedFolder, error) {
	// Step 1: Get media folder list to find mount paths
	mq := url.Values{}
	mq.Set("func", "media_folder_list")

	var mediaResp struct {
		Status int    `json:"status"`
		Count  int    `json:"count"`
		Datas  []struct {
			Path       string `json:"path"`
			MountPath  string `json:"mount_path"`
			FolderPath string `json:"folder_path"`
		} `json:"datas"`
	}
	if err := c.fileStationJSON(mq, &mediaResp); err != nil {
		return nil, err
	}

	// Step 2: Also probe well-known share names as the media_folder_list
	// may not include all shares (only media-indexed ones).
	shareNames := []string{
		"Public", "Download", "Multimedia", "homes", "Backup",
		"Web", "Container", "HybridDesk", "QPKG", "Usb",
		"home", "Network Recycle Bin 1",
	}

	// Collect share names from media folder list
	mediaShares := map[string]string{} // name -> mount_path
	for _, d := range mediaResp.Datas {
		// folder_path is like "Download/" or "Multimedia/"
		name := strings.TrimSuffix(d.FolderPath, "/")
		// Avoid nested paths (only top-level shares)
		if !strings.Contains(name, "/") && name != "" {
			mediaShares[name] = d.MountPath
		}
	}
	for name := range mediaShares {
		shareNames = append(shareNames, name)
	}

	// Deduplicate
	seen := map[string]bool{}
	unique := []string{}
	for _, n := range shareNames {
		if !seen[n] {
			seen[n] = true
			unique = append(unique, n)
		}
	}

	var folders []SharedFolder
	for _, name := range unique {
		lq := url.Values{}
		lq.Set("func", "get_list")
		lq.Set("path", "/"+name)
		lq.Set("list_mode", "all")
		lq.Set("start", "0")
		lq.Set("limit", "1")
		lq.Set("dir", "ASC")
		lq.Set("sort", "filename")

		var listResp struct {
			Total     int `json:"total"`
			RealTotal int `json:"real_total"`
			Datas     []struct {
				Filename string `json:"filename"`
			} `json:"datas"`
		}
		if err := c.fileStationJSON(lq, &listResp); err != nil {
			continue
		}
		// If total > 0 or we got a valid response (total can be 0 for empty shares)
		// A status=5 response won't unmarshal into Total correctly
		// Check: if we got a valid response, add the share
		mountPath := ""
		if mp, ok := mediaShares[name]; ok {
			mountPath = mp + "/" + name + "/"
		}

		folders = append(folders, SharedFolder{
			Name:      name,
			Path:      "/" + name,
			VolumeID:  mountPath,
			ItemCount: listResp.Total,
		})
	}
	return folders, nil
}

// Apps fetches installed QPKG apps via the File Station media folder list.
// QNAP does not expose a public CGI for QPKG list; we return what we can discover.
func (c *Client) Apps() ([]App, error) {
	// Try to list QPKG folder contents to discover installed apps
	q := url.Values{}
	q.Set("func", "get_list")
	q.Set("path", "/QPKG")
	q.Set("list_mode", "all")
	q.Set("start", "0")
	q.Set("limit", "200")
	q.Set("dir", "ASC")
	q.Set("sort", "filename")
	q.Set("type", "4") // folders only

	var resp struct {
		Total int `json:"total"`
		Datas []struct {
			Filename string `json:"filename"`
			IsFolder int    `json:"isfolder"`
		} `json:"datas"`
	}
	if err := c.fileStationJSON(q, &resp); err != nil {
		return []App{}, nil
	}
	apps := make([]App, 0, len(resp.Datas))
	for _, d := range resp.Datas {
		if d.IsFolder == 1 {
			apps = append(apps, App{
				Name:        d.Filename,
				DisplayName: d.Filename,
				Enabled:     true,
				Status:      "installed",
				Location:    "/QPKG/" + d.Filename,
			})
		}
	}
	return apps, nil
}

// Volumes — not available via CGI on this device; returns empty list.
func (c *Client) Volumes() ([]Volume, error) {
	return []Volume{}, nil
}

// StoragePools — not available via CGI on this device; returns empty list.
func (c *Client) StoragePools() ([]StoragePool, error) {
	return []StoragePool{}, nil
}

// ISCSITargets — not available via CGI on this device; returns empty list.
func (c *Client) ISCSITargets() ([]ISCSITarget, error) {
	return []ISCSITarget{}, nil
}

// ISCSILuns — not available via CGI on this device; returns empty list.
func (c *Client) ISCSILuns() ([]ISCSILun, error) {
	return []ISCSILun{}, nil
}

// Snapshots — not available via CGI on this device; returns empty list.
func (c *Client) Snapshots() ([]Snapshot, error) {
	return []Snapshot{}, nil
}

// Containers — not available via CGI on this device; returns empty list.
func (c *Client) Containers() ([]Container, error) {
	return []Container{}, nil
}

// Projects — not available via CGI on this device; returns empty list.
func (c *Client) Projects() ([]Project, error) {
	return []Project{}, nil
}

// GetJSON performs a GET against an arbitrary path (kept for datasource compatibility).
func (c *Client) GetJSON(path string, query url.Values, v interface{}) error {
	body, err := c.cgiGet(path, query)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("qnap GET %s unmarshal: %w", path, err)
	}
	return nil
}

// -----------------------------------------------------------------------
// XML helpers
// -----------------------------------------------------------------------

// xmlField extracts the text content of the first occurrence of <tag>text</tag>.
func xmlField(data []byte, tag string) string {
	return xmlFieldFromStr(string(data), tag)
}

// xmlFieldFromStr extracts a field from an XML string.
func xmlFieldFromStr(s, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(s, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(s[start:], close)
	if end < 0 {
		return ""
	}
	val := s[start : start+end]
	if strings.HasPrefix(val, "<![CDATA[") && strings.HasSuffix(val, "]]>") {
		val = val[9 : len(val)-3]
	}
	return val
}

// xmlFieldN extracts a numbered field like <ifname1>, <ifname2>, etc.
func xmlFieldN(s, tag string, n int) string {
	return xmlFieldFromStr(s, tag+strconv.Itoa(n))
}

// extractBlock extracts the content between <tag> and </tag>.
func extractBlock(s, tag string) string {
	return xmlFieldFromStr(s, tag)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
