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
//  4. iSCSI CGI — http://<host>:8080/cgi-bin/disk/iscsi_portal_setting.cgi
//     XML responses. Requires a separate SID obtained via auth on port 8080.
//     HTTPS SIDs return errorcode -22 on this endpoint.
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
	Host        string
	BaseURL     string
	BaseURL8080 string // http://<host>:8080 — required for iSCSI CGI
	Username    string
	httpClient  *http.Client
	sid         string
	sid8080     string // SID obtained via auth on port 8080
	password    string // stored for lazy 8080 login
}

// New creates a new QNAP client and authenticates immediately.
func New(host, username, password string, sslInsecure bool) (*Client, error) {
	baseURL := host
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		baseURL = "https://" + host
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Derive host without scheme for port-8080 URL
	hostOnly := host
	if strings.HasPrefix(hostOnly, "https://") {
		hostOnly = hostOnly[8:]
	} else if strings.HasPrefix(hostOnly, "http://") {
		hostOnly = hostOnly[7:]
	}
	hostOnly = strings.TrimRight(hostOnly, "/")
	// Strip any existing port so we can add 8080
	if idx := strings.LastIndex(hostOnly, ":"); idx > strings.LastIndex(hostOnly, "]") {
		hostOnly = hostOnly[:idx]
	}

	c := &Client{
		Host:        host,
		BaseURL:     baseURL,
		BaseURL8080: "http://" + hostOnly + ":8080",
		Username:    username,
		password:    password,
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

// login8080 authenticates on port 8080 (required for iSCSI CGI) and stores sid8080.
func (c *Client) login8080() error {
	encoded := base64.StdEncoding.EncodeToString([]byte(c.password))
	endpoint := fmt.Sprintf("%s%s", c.BaseURL8080, pathLogin)
	params := url.Values{}
	params.Set("user", c.Username)
	params.Set("pwd", encoded)
	params.Set("serviceKey", "1")

	resp, err := c.httpClient.PostForm(endpoint, params)
	if err != nil {
		return fmt.Errorf("qnap port-8080 login POST failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("qnap port-8080 login read body: %w", err)
	}

	sid := xmlField(body, "authSid")
	passed := xmlField(body, "authPassed")
	if passed != "1" || sid == "" {
		return fmt.Errorf("qnap port-8080 authentication failed (authPassed=%q)", passed)
	}
	c.sid8080 = sid
	return nil
}

// iscsiPost posts to the iSCSI CGI on port 8080 with the given query params and body params.
// Lazily authenticates on port 8080 on first call.
func (c *Client) iscsiPost(query url.Values, body url.Values) ([]byte, error) {
	if c.sid8080 == "" {
		if err := c.login8080(); err != nil {
			return nil, err
		}
	}
	query.Set("sid", c.sid8080)
	endpoint := fmt.Sprintf("%s/cgi-bin/disk/iscsi_portal_setting.cgi?%s",
		c.BaseURL8080, query.Encode())

	resp, err := c.httpClient.PostForm(endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("qnap iSCSI POST failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qnap iSCSI POST read body: %w", err)
	}
	if xmlField(data, "authPassed") != "1" {
		return nil, fmt.Errorf("qnap iSCSI: not authenticated (errorcode=%s)", xmlField(data, "errorcode"))
	}
	return data, nil
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

// ISCSITarget represents an iSCSI target from the QNAP iSCSI CGI API.
type ISCSITarget struct {
	ID         string
	Name       string
	Alias      string
	IQN        string
	Status     string
	Enabled    bool
	Initiators []string // connected initiator IQNs (deduplicated)
}

// ISCSILun represents an iSCSI LUN from the QNAP iSCSI CGI API.
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
	SerialNum string
	NAA       string
	Enabled   bool
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

// ISCSITargets fetches iSCSI targets via the port-8080 CGI API.
func (c *Client) ISCSITargets() ([]ISCSITarget, error) {
	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "extra_get")
	q.Set("targetList", "1")

	data, err := c.iscsiPost(q, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("qnap ISCSITargets: %w", err)
	}

	s := string(data)
	// Extract each <targetInfo>...</targetInfo> block
	var targets []ISCSITarget
	const openTag = "<targetInfo>"
	const closeTag = "</targetInfo>"
	offset := 0
	for {
		start := strings.Index(s[offset:], openTag)
		if start < 0 {
			break
		}
		start += offset
		end := strings.Index(s[start:], closeTag)
		if end < 0 {
			break
		}
		block := s[start+len(openTag) : start+end]
		offset = start + end + len(closeTag)

		idx := xmlFieldFromStr(block, "targetIndex")
		name := xmlFieldFromStr(block, "targetName")
		iqn := xmlFieldFromStr(block, "targetIQN")
		alias := xmlFieldFromStr(block, "targetAlias")
		status := xmlFieldFromStr(block, "targetStatus")

		// Collect unique connected initiator IQNs
		var initiators []string
		seen := map[string]bool{}
		connBlock := xmlFieldFromStr(block, "initiatorConnList")
		connOffset := 0
		const connOpen = "<initiatorConnInfo>"
		const connClose = "</initiatorConnInfo>"
		for {
			cs := strings.Index(connBlock[connOffset:], connOpen)
			if cs < 0 {
				break
			}
			cs += connOffset
			ce := strings.Index(connBlock[cs:], connClose)
			if ce < 0 {
				break
			}
			cb := connBlock[cs+len(connOpen) : cs+ce]
			connOffset = cs + ce + len(connClose)
			iqnConn := xmlFieldFromStr(cb, "initiatorIQN")
			if iqnConn != "" && !seen[iqnConn] {
				seen[iqnConn] = true
				initiators = append(initiators, iqnConn)
			}
		}

		targets = append(targets, ISCSITarget{
			ID:         idx,
			Name:       name,
			IQN:        iqn,
			Alias:      alias,
			Status:     status,
			Enabled:    status == "1",
			Initiators: initiators,
		})
	}
	return targets, nil
}

// ISCSILuns fetches iSCSI LUNs via the port-8080 CGI API.
// First retrieves the LUN index list, then fetches detail for each LUN.
func (c *Client) ISCSILuns() ([]ISCSILun, error) {
	// Step 1: get LUN index list
	q := url.Values{}
	q.Set("store", "lunList")
	body := url.Values{}
	body.Set("prod", "qts")
	body.Set("proto", "iscsi")
	body.Set("target", "lio")
	body.Set("backend", "dm")
	body.Set("conf", "ini")
	body.Set("func", "extra_get")
	body.Set("extra_lun_index", "1")

	data, err := c.iscsiPost(q, body)
	if err != nil {
		return nil, fmt.Errorf("qnap ISCSILuns list: %w", err)
	}

	// Parse LUN indexes from <LUNInfo><row>...<LUNIndex>N</LUNIndex>...</row></LUNInfo>
	var lunIndexes []string
	s := string(data)
	offset := 0
	const rowOpen = "<row>"
	const rowClose = "</row>"
	for {
		rs := strings.Index(s[offset:], rowOpen)
		if rs < 0 {
			break
		}
		rs += offset
		re := strings.Index(s[rs:], rowClose)
		if re < 0 {
			break
		}
		rb := s[rs+len(rowOpen) : rs+re]
		offset = rs + re + len(rowClose)
		if idx := xmlFieldFromStr(rb, "LUNIndex"); idx != "" {
			lunIndexes = append(lunIndexes, idx)
		}
	}

	// Step 2: fetch detail for each LUN
	var luns []ISCSILun
	for _, lunID := range lunIndexes {
		dq := url.Values{}
		dq.Set("prod", "qts")
		dq.Set("proto", "iscsi")
		dq.Set("target", "lio")
		dq.Set("backend", "dm")
		dq.Set("conf", "ini")
		dq.Set("func", "extra_get")
		dq.Set("lun_info", "1")
		dq.Set("lunID", lunID)

		dd, err := c.iscsiPost(dq, url.Values{"lastUpdateTime": []string{"0"}})
		if err != nil {
			continue
		}
		ds := string(dd)

		// Parse the first <row> block in <LUNInfo>
		rs := strings.Index(ds, rowOpen)
		if rs < 0 {
			continue
		}
		re := strings.Index(ds[rs:], rowClose)
		if re < 0 {
			continue
		}
		rb := ds[rs+len(rowOpen) : rs+re]

		capacityBytes, _ := strconv.ParseInt(xmlFieldFromStr(rb, "capacity_bytes"), 10, 64)
		lunIdx, _ := strconv.Atoi(xmlFieldFromStr(rb, "LUNIndex"))
		status := xmlFieldFromStr(rb, "LUNStatus")
		enabled := xmlFieldFromStr(rb, "LUNEnable") == "1"
		thinProv := xmlFieldFromStr(rb, "LUNThinAllocate") == "1"

		// Determine target ID from LUNTargetList
		targetIdx := xmlFieldFromStr(xmlFieldFromStr(rb, "LUNTargetList"), "targetIndex")

		lun := ISCSILun{
			ID:        lunID,
			Name:      xmlFieldFromStr(rb, "LUNName"),
			TargetID:  targetIdx,
			LunID:     lunIdx,
			SizeBytes: capacityBytes,
			Status:    status,
			ThinProv:  thinProv,
			FilePath:  xmlFieldFromStr(rb, "LUNPath"),
			SerialNum: xmlFieldFromStr(rb, "LUNSerialNum"),
			NAA:       xmlFieldFromStr(rb, "LUNNAA"),
			Enabled:   enabled,
		}
		luns = append(luns, lun)
	}
	return luns, nil
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

// -----------------------------------------------------------------------
// Write methods — SharedFolder
// -----------------------------------------------------------------------

// SharedFolderCreateInput holds the parameters for creating a shared folder.
type SharedFolderCreateInput struct {
	Name        string
	VolumeID    string // e.g. "CACHEDEV1_DATA"
	Comment     string
	Compression bool
	Hidden      bool
	ReadOnly    bool
}

// SharedFolderUpdateInput holds the mutable fields for an existing shared folder.
type SharedFolderUpdateInput struct {
	Name        string // used to identify the folder
	Comment     string
	Compression bool
	Hidden      bool
	ReadOnly    bool
}

// CreateSharedFolder creates a new shared folder via the File Station CGI.
func (c *Client) CreateSharedFolder(input SharedFolderCreateInput) error {
	q := url.Values{}
	q.Set("func", "add_share")

	q.Set("sharename", input.Name)
	if input.VolumeID != "" {
		q.Set("vol_no", input.VolumeID)
	}
	if input.Comment != "" {
		q.Set("comment", input.Comment)
	}
	if input.Compression {
		q.Set("compress", "1")
	} else {
		q.Set("compress", "0")
	}
	if input.Hidden {
		q.Set("hidden", "1")
	} else {
		q.Set("hidden", "0")
	}
	if input.ReadOnly {
		q.Set("readonly", "1")
	} else {
		q.Set("readonly", "0")
	}

	var resp struct {
		Status int `json:"status"`
	}
	if err := c.fileStationJSON(q, &resp); err != nil {
		return fmt.Errorf("qnap CreateSharedFolder: %w", err)
	}
	if resp.Status != 1 {
		return fmt.Errorf("qnap CreateSharedFolder: unexpected status %d", resp.Status)
	}
	return nil
}

// UpdateSharedFolder updates a shared folder's mutable attributes.
func (c *Client) UpdateSharedFolder(input SharedFolderUpdateInput) error {
	q := url.Values{}
	q.Set("func", "edit_share")
	q.Set("sharename", input.Name)
	q.Set("comment", input.Comment)
	if input.Compression {
		q.Set("compress", "1")
	} else {
		q.Set("compress", "0")
	}
	if input.Hidden {
		q.Set("hidden", "1")
	} else {
		q.Set("hidden", "0")
	}
	if input.ReadOnly {
		q.Set("readonly", "1")
	} else {
		q.Set("readonly", "0")
	}

	var resp struct {
		Status int `json:"status"`
	}
	if err := c.fileStationJSON(q, &resp); err != nil {
		return fmt.Errorf("qnap UpdateSharedFolder: %w", err)
	}
	if resp.Status != 1 {
		return fmt.Errorf("qnap UpdateSharedFolder: unexpected status %d", resp.Status)
	}
	return nil
}

// DeleteSharedFolder removes a shared folder by name.
func (c *Client) DeleteSharedFolder(name string) error {
	q := url.Values{}
	q.Set("func", "delete_share")
	q.Set("sharename", name)

	var resp struct {
		Status int `json:"status"`
	}
	if err := c.fileStationJSON(q, &resp); err != nil {
		return fmt.Errorf("qnap DeleteSharedFolder: %w", err)
	}
	if resp.Status != 1 {
		return fmt.Errorf("qnap DeleteSharedFolder: unexpected status %d", resp.Status)
	}
	return nil
}

// -----------------------------------------------------------------------
// Write methods — ISCSITarget
// -----------------------------------------------------------------------

// ISCSITargetCreateInput holds parameters for creating an iSCSI target.
type ISCSITargetCreateInput struct {
	Name    string
	Alias   string
	IQN     string // optional; if empty QNAP generates one
	Enabled bool
}

// ISCSITargetUpdateInput holds mutable fields for an existing iSCSI target.
type ISCSITargetUpdateInput struct {
	ID      string
	Name    string
	Alias   string
	Enabled bool
}

// iscsiEnsureAuth re-authenticates on port 8080 if the session looks expired,
// then retries the operation once.
func (c *Client) iscsiEnsureAuth() error {
	if c.sid8080 == "" {
		return c.login8080()
	}
	return nil
}

// CreateISCSITarget creates an iSCSI target via the port-8080 CGI.
func (c *Client) CreateISCSITarget(input ISCSITargetCreateInput) (*ISCSITarget, error) {
	if err := c.iscsiEnsureAuth(); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "create_target")

	body := url.Values{}
	body.Set("targetName", input.Name)
	body.Set("targetAlias", input.Alias)
	if input.IQN != "" {
		body.Set("targetIQN", input.IQN)
	}
	enabled := "0"
	if input.Enabled {
		enabled = "1"
	}
	body.Set("targetEnable", enabled)

	data, err := c.iscsiPost(q, body)
	if err != nil {
		// Session may have expired — retry once
		if rerr := c.login8080(); rerr == nil {
			data, err = c.iscsiPost(q, body)
		}
		if err != nil {
			return nil, fmt.Errorf("qnap CreateISCSITarget: %w", err)
		}
	}

	if ec := xmlField(data, "errorcode"); ec != "" && ec != "0" {
		return nil, fmt.Errorf("qnap CreateISCSITarget: errorcode=%s", ec)
	}

	// Fetch the newly created target by name
	targets, err := c.ISCSITargets()
	if err != nil {
		return nil, fmt.Errorf("qnap CreateISCSITarget: post-create read: %w", err)
	}
	for _, t := range targets {
		if t.Name == input.Name {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("qnap CreateISCSITarget: target %q not found after creation", input.Name)
}

// UpdateISCSITarget updates an existing iSCSI target via the port-8080 CGI.
func (c *Client) UpdateISCSITarget(input ISCSITargetUpdateInput) error {
	if err := c.iscsiEnsureAuth(); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "edit_target")

	body := url.Values{}
	body.Set("targetIndex", input.ID)
	body.Set("targetName", input.Name)
	body.Set("targetAlias", input.Alias)
	enabled := "0"
	if input.Enabled {
		enabled = "1"
	}
	body.Set("targetEnable", enabled)

	data, err := c.iscsiPost(q, body)
	if err != nil {
		if rerr := c.login8080(); rerr == nil {
			data, err = c.iscsiPost(q, body)
		}
		if err != nil {
			return fmt.Errorf("qnap UpdateISCSITarget: %w", err)
		}
	}

	if ec := xmlField(data, "errorcode"); ec != "" && ec != "0" {
		return fmt.Errorf("qnap UpdateISCSITarget: errorcode=%s", ec)
	}
	return nil
}

// DeleteISCSITarget deletes an iSCSI target by its index ID.
func (c *Client) DeleteISCSITarget(id string) error {
	if err := c.iscsiEnsureAuth(); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "delete_target")

	body := url.Values{}
	body.Set("targetIndex", id)

	data, err := c.iscsiPost(q, body)
	if err != nil {
		if rerr := c.login8080(); rerr == nil {
			data, err = c.iscsiPost(q, body)
		}
		if err != nil {
			return fmt.Errorf("qnap DeleteISCSITarget: %w", err)
		}
	}

	if ec := xmlField(data, "errorcode"); ec != "" && ec != "0" {
		return fmt.Errorf("qnap DeleteISCSITarget: errorcode=%s", ec)
	}
	return nil
}

// -----------------------------------------------------------------------
// Write methods — ISCSILun
// -----------------------------------------------------------------------

// ISCSILunCreateInput holds parameters for creating an iSCSI LUN.
type ISCSILunCreateInput struct {
	Name      string
	TargetID  string // target index to map LUN to
	SizeBytes int64
	ThinProv  bool
	VolumeID  string // storage volume/pool path, e.g. "CACHEDEV1_DATA"
}

// ISCSILunUpdateInput holds mutable fields for an existing iSCSI LUN.
type ISCSILunUpdateInput struct {
	ID        string
	Name      string
	SizeBytes int64 // can only grow
	Enabled   bool
}

// CreateISCSILun creates an iSCSI LUN and maps it to a target.
func (c *Client) CreateISCSILun(input ISCSILunCreateInput) (*ISCSILun, error) {
	if err := c.iscsiEnsureAuth(); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "create_lun")

	thinAlloc := "0"
	if input.ThinProv {
		thinAlloc = "1"
	}

	body := url.Values{}
	body.Set("LUNName", input.Name)
	body.Set("LUNThinAllocate", thinAlloc)
	body.Set("LUNCapacity", strconv.FormatInt(input.SizeBytes, 10))
	if input.VolumeID != "" {
		body.Set("LUNLocation", "/share/"+input.VolumeID)
	}
	if input.TargetID != "" {
		body.Set("targetIndex", input.TargetID)
	}

	data, err := c.iscsiPost(q, body)
	if err != nil {
		if rerr := c.login8080(); rerr == nil {
			data, err = c.iscsiPost(q, body)
		}
		if err != nil {
			return nil, fmt.Errorf("qnap CreateISCSILun: %w", err)
		}
	}

	if ec := xmlField(data, "errorcode"); ec != "" && ec != "0" {
		return nil, fmt.Errorf("qnap CreateISCSILun: errorcode=%s", ec)
	}

	// Fetch the newly created LUN by name
	luns, err := c.ISCSILuns()
	if err != nil {
		return nil, fmt.Errorf("qnap CreateISCSILun: post-create read: %w", err)
	}
	for _, l := range luns {
		if l.Name == input.Name {
			return &l, nil
		}
	}
	return nil, fmt.Errorf("qnap CreateISCSILun: LUN %q not found after creation", input.Name)
}

// UpdateISCSILun updates an existing iSCSI LUN (rename, resize, enable/disable).
func (c *Client) UpdateISCSILun(input ISCSILunUpdateInput) error {
	if err := c.iscsiEnsureAuth(); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "edit_lun")

	enabled := "0"
	if input.Enabled {
		enabled = "1"
	}

	body := url.Values{}
	body.Set("LUNIndex", input.ID)
	body.Set("LUNName", input.Name)
	body.Set("LUNEnable", enabled)
	if input.SizeBytes > 0 {
		body.Set("LUNCapacity", strconv.FormatInt(input.SizeBytes, 10))
	}

	data, err := c.iscsiPost(q, body)
	if err != nil {
		if rerr := c.login8080(); rerr == nil {
			data, err = c.iscsiPost(q, body)
		}
		if err != nil {
			return fmt.Errorf("qnap UpdateISCSILun: %w", err)
		}
	}

	if ec := xmlField(data, "errorcode"); ec != "" && ec != "0" {
		return fmt.Errorf("qnap UpdateISCSILun: errorcode=%s", ec)
	}
	return nil
}

// DeleteISCSILun deletes an iSCSI LUN by its index ID.
func (c *Client) DeleteISCSILun(id string) error {
	if err := c.iscsiEnsureAuth(); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("prod", "qts")
	q.Set("proto", "iscsi")
	q.Set("target", "lio")
	q.Set("backend", "dm")
	q.Set("conf", "ini")
	q.Set("func", "delete_lun")

	body := url.Values{}
	body.Set("LUNIndex", id)

	data, err := c.iscsiPost(q, body)
	if err != nil {
		if rerr := c.login8080(); rerr == nil {
			data, err = c.iscsiPost(q, body)
		}
		if err != nil {
			return fmt.Errorf("qnap DeleteISCSILun: %w", err)
		}
	}

	if ec := xmlField(data, "errorcode"); ec != "" && ec != "0" {
		return fmt.Errorf("qnap DeleteISCSILun: errorcode=%s", ec)
	}
	return nil
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
