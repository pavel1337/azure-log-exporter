package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/ghodss/yaml"
	"github.com/go-redis/redis"
	"github.com/pavel1337/go-msgraph"
	"github.com/robertkowalski/graylog-golang"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

var pathError *os.PathError

type Config struct {
	RedisAddress     string        `json:"redis_address"`
	RedisExpiration  time.Duration `json:"redis_expiration"`
	GraylogHost      string        `json:"graylog_host"`
	GraylogPort      int           `json:"graylog_port"`
	TenantID         string        `json:"msgraph_tenantID"`
	ApplicationID    string        `json:"msgraph_appID"`
	SecretKey        string        `json:"msgraph_secretKey"`
	AppNameInGraylog string        `json:"app_name_in_graylog"`
	AbuseIPDBApiKey  string        `json:"abuseipdb_apikey"`
}

type IPLookUpData struct {
	Status      string `json:"status"`
	Description string `json:"description"`
	Data        struct {
		Geo struct {
			Host          string      `json:"host"`
			IP            string      `json:"ip"`
			Rdns          string      `json:"rdns"`
			Asn           int         `json:"asn"`
			Isp           string      `json:"isp"`
			CountryName   string      `json:"country_name"`
			CountryCode   string      `json:"country_code"`
			RegionName    string      `json:"region_name"`
			RegionCode    string      `json:"region_code"`
			City          string      `json:"city"`
			PostalCode    string      `json:"postal_code"`
			ContinentName string      `json:"continent_name"`
			ContinentCode string      `json:"continent_code"`
			Latitude      float64     `json:"latitude"`
			Longitude     float64     `json:"longitude"`
			MetroCode     interface{} `json:"metro_code"`
			Timezone      string      `json:"timezone"`
			Datetime      string      `json:"datetime"`
		} `json:"geo"`
	} `json:"data"`
}

type GELFInstance struct {
	Timestamp            int64  `json:"timestamp"`
	ID                   string `json:"_signin_id"`
	Host                 string `json:"host"`
	AppName              string `json:"application_name"`
	ShortMessage         string `json:"short_message"`
	UserPrincipalName    string `json:"_user_Principal_Name"`
	UserDisplayName      string `json:"_user_Display_Name"`
	AppDisplayName       string `json:"_app_Display_Name"`
	IpAddress            string `json:"_Ip_Address"`
	ClientAppUsed        string `json:"_client_App_Used"`
	ResourceDisplayName  string `json:"_resourse_Display_Name"`
	DeviceDetail         string `json:"_device_detail"`
	Location             string `json:"_location"`
	LocationCity         string `json:"_location_city"`
	LocationState        string `json:"_location_state"`
	LocationCountry      string `json:"_location_country"`
	GeoData              string `json:"_geodata"`
	StatusCode           int    `json:"_status_code"`
	StatusDescripton     string `json:"_status_descripton"`
	AbuseConfidenceScore int    `json:"_abuseConfidenceScore"`
	TotalReports         int    `json:"_totalReports"`
}

type AbuseIPDBData struct {
	IPAddress            string    `json:"ipAddress"`
	IsPublic             bool      `json:"isPublic"`
	IPVersion            int       `json:"ipVersion"`
	IsWhitelisted        bool      `json:"isWhitelisted"`
	AbuseConfidenceScore int       `json:"abuseConfidenceScore"`
	CountryCode          string    `json:"countryCode"`
	UsageType            string    `json:"usageType"`
	Isp                  string    `json:"isp"`
	Domain               string    `json:"domain"`
	TotalReports         int       `json:"totalReports"`
	NumDistinctUsers     int       `json:"numDistinctUsers"`
	LastReportedAt       time.Time `json:"lastReportedAt"`
}

func main() {
	path := parseFlags()

	c, err := parseConfig(*path)
	if err != nil {
		log.Fatalf("ERROR: %s", err)
		return
	}

	graphClient, err := msgraph.NewGraphClient(c.TenantID, c.ApplicationID, c.SecretKey)
	if err != nil {
		fmt.Println(err)
	}

	rc := redis.NewClient(&redis.Options{
		Addr:     c.RedisAddress,
		Password: "", // no password set
		DB:       1,  // use default DB
	})
	dur := c.RedisExpiration * time.Hour

	t := time.Now().UTC().Add(time.Minute * -10)
	filter := "createdDateTime ge " + t.Format("2006-01-02T15:04:05Z")

	var gc gelf.Config
	gc.GraylogPort = c.GraylogPort
	gc.GraylogHostname = c.GraylogHost
	g := gelf.New(gc)

	for {
		t = time.Now().UTC().Add(time.Minute * -10)
		list, err := graphClient.ListSignInsWithFilter(filter)
		if err != nil {
			fmt.Println(err)
		}
		for i := range list {
			signin := list[i]
			i, _ := rc.Exists(signin.ID).Result()
			if i == 0 {
				_, err := rc.Set(signin.ID, 1, dur).Result()
				if err != nil {
					log.Println(err)
				}
				msg, _ := NewGelfLog(signin, c.AppNameInGraylog, c.AbuseIPDBApiKey)
				g.Log(string(msg))
			}
		}
		filter = "createdDateTime ge " + t.Format("2006-01-02T15:04:05Z")
		time.Sleep(1 * time.Minute)
	}

}

func Ipv6LookUp(ip string) IPLookUpData {

	defer func() {
		if r := recover(); r != nil {
		}
	}()

	c := http.Client{
		Timeout: time.Second * 10, // Maximum of 10 secs
	}

	url := "https://tools.keycdn.com/geo.json?host=" + ip

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Panic("client error:", err)
	}

	res, getErr := c.Do(req)
	if getErr != nil {
		log.Panic("request error:", getErr)
	}

	body, readErr := ioutil.ReadAll(res.Body)
	if readErr != nil {
		log.Panic("response error:", readErr)
	}

	data := IPLookUpData{}
	jsonErr := json.Unmarshal(body, &data)
	if jsonErr != nil {
		log.Panic("json parse error:", jsonErr)
	}

	return data
}

func NewGelfLog(s msgraph.Signin, AppNameInGraylog string, abuseipdb_apikey string) ([]byte, error) {
	gi := GELFInstance{}
	gi.Timestamp = s.CreatedDateTime.Unix()
	hn, err := os.Hostname()
	if err != nil {
		fmt.Errorf("Hostname problem: %v", err)
		os.Exit(1)
	}
	gi.AppName = AppNameInGraylog
	gi.Host = hn
	gi.DeviceDetail = s.DeviceDetail.OperatingSystem + " " + s.DeviceDetail.Browser
	gi.ID = s.ID
	gi.UserDisplayName = s.UserDisplayName
	gi.UserPrincipalName = s.UserPrincipalName
	gi.AppDisplayName = s.AppDisplayName
	if IsIpv4Net(s.IpAddress) {
		gi.IpAddress = s.IpAddress
		gi.Location = s.Location.City + " " + s.Location.State + " " + s.Location.CountryOrRegion
		gi.LocationCity = s.Location.City
		gi.LocationState = s.Location.State
		gi.LocationCountry = s.Location.CountryOrRegion
		gi.GeoData = strconv.FormatFloat(s.Location.GeoCoordinates.Latitude, 'f', 6, 64) + "," + strconv.FormatFloat(s.Location.GeoCoordinates.Longitude, 'f', 6, 64)
		gi.ShortMessage = s.UserDisplayName + " from " + gi.Location + " with " + gi.DeviceDetail + " via " + s.ResourceDisplayName
	} else {
		d := Ipv6LookUp(s.IpAddress)
		gi.IpAddress = s.IpAddress
		gi.Location = d.Data.Geo.City + " " + d.Data.Geo.RegionName + " " + d.Data.Geo.CountryCode
		gi.LocationCity = d.Data.Geo.City
		gi.LocationState = d.Data.Geo.RegionName
		gi.LocationCountry = d.Data.Geo.CountryCode
		gi.GeoData = strconv.FormatFloat(d.Data.Geo.Latitude, 'f', 6, 64) + "," + strconv.FormatFloat(d.Data.Geo.Longitude, 'f', 6, 64)
		gi.ShortMessage = s.UserDisplayName + " from " + gi.Location + " with " + gi.DeviceDetail + " via " + s.ResourceDisplayName
	}

	abuseipdb := GetAbuseIPDBData(s.IpAddress, abuseipdb_apikey)
	gi.AbuseConfidenceScore = abuseipdb.AbuseConfidenceScore
	gi.TotalReports = abuseipdb.TotalReports

	gi.StatusCode = s.Status.ErrorCode
	if s.Status.ErrorCode != 0 {
		gi.StatusDescripton = s.Status.FailureReason + s.Status.AdditionalDetails
	}
	gi.ClientAppUsed = s.ClientAppUsed
	gi.ResourceDisplayName = s.ResourceDisplayName

	message, err := json.Marshal(gi)
	return message, err
}

func GetAbuseIPDBData(ip string, apikey string) AbuseIPDBData {
	baseUrl, _ := url.Parse("https://api.abuseipdb.com/api/v2/check")
	params := url.Values{}
	params.Add("ipAddress", ip)
	params.Add("maxAgeInDays", "365")
	baseUrl.RawQuery = params.Encode()

	req, err := http.NewRequest("GET", baseUrl.String(), nil)
	if err != nil {
		log.Panic("client error:", err)
	}

	req.Header.Set("Key", apikey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, getErr := http.DefaultClient.Do(req)
	if err != nil {
		log.Panic("request error:", getErr)
	}
	defer resp.Body.Close()

	respBody, readErr := ioutil.ReadAll(resp.Body)
	if readErr != nil {
		log.Panic("response error:", readErr)
	}

	var marsh struct {
		AbuseIPDBData AbuseIPDBData `json:"data"`
	}
	jsonErr := json.Unmarshal(respBody, &marsh)
	if jsonErr != nil {
		log.Panic("json parse error:", jsonErr)
	}

	return marsh.AbuseIPDBData
}

func IsIpv4Net(host string) bool {
	testInput := net.ParseIP(host)
	if testInput.To4() == nil {
		return false
	}
	return true
}

func parseFlags() *string {
	configPathHelpInfo := " path to config file"
	configPath := flag.String("c", "", configPathHelpInfo)
	flag.Parse()
	return configPath
}

func parseConfig(p string) (Config, error) {
	var c Config
	rawConfig, err := ioutil.ReadFile(p)
	if err != nil {
		flag.Usage()
		if errors.As(err, &pathError) {
			return c, errors.New("Please create '/etc/azure-log-exporter/config.yml'\nExample:\n   redis_address: '1.1.1.1:6369'\n   redis_expiration: 1 (in hours)\n   graylog_host: '1.1.1.1'\n   graylog_port: 12201\n   msgraph_tenantID: '<TenantID>' \n   msgraph_appID: '<ApplicationID>' \n   msgraph_secretKey: '<secretkey>' \n   app_name_in_graylog: 'name_you_want_to_see_in_application_name_field' \n")
		}
		return c, err
	}
	err = yaml.Unmarshal(rawConfig, &c)
	if err != nil {
		return c, err
	}
	return c, nil
}
