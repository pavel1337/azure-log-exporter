package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/go-redis/redis"
	"github.com/pavel1337/azure-log-exporter/models"
	"github.com/pavel1337/go-msgraph"
	gelf "github.com/robertkowalski/graylog-golang"
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
	Isp                  string `json:"_ipinfo_isp"`
}

type application struct {
	msGraphClient *msgraph.GraphClient
	rcLogins      *redis.Client
	rcAbuseDB     *redis.Client
	rcIpLookUp    *redis.Client
	gelf          *gelf.Gelf
	errorLog      *log.Logger
	config        Config
}

func redisNewRc(addr string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "", // no password set
		DB:       db,
	})
}

func main() {
	errorLog := log.New(os.Stderr, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile)

	path := parseFlags()
	c, err := parseConfig(*path)
	if err != nil {
		errorLog.Println(err)
		return
	}
	msGraphClient, err := msgraph.NewGraphClient(c.TenantID, c.ApplicationID, c.SecretKey)
	if err != nil {
		errorLog.Println(err)
		return
	}

	var gc gelf.Config
	gc.GraylogPort = c.GraylogPort
	gc.GraylogHostname = c.GraylogHost
	g := gelf.New(gc)

	for i := 1; i <= 3; i++ {
		_, err = redisNewRc(c.RedisAddress, 1).Ping().Result()
		if err != nil {
			errorLog.Println(err)
			return
		}
	}

	app := application{
		msGraphClient: msGraphClient,
		rcLogins:      redisNewRc(c.RedisAddress, 1),
		rcAbuseDB:     redisNewRc(c.RedisAddress, 2),
		rcIpLookUp:    redisNewRc(c.RedisAddress, 3),
		gelf:          g,
		errorLog:      errorLog,
		config:        c,
	}

	for {
		time.Sleep(1 * time.Minute)
		t := time.Now().UTC().Add(time.Minute * -10)
		filter := "createdDateTime ge " + t.Format("2006-01-02T15:04:05Z")
		signins, err := app.msGraphClient.ListSignInsWithFilter(filter)
		if err != nil {
			app.errorLog.Println(err)
			continue
		}
		for _, signin := range signins {
			i, err := app.rcLogins.Exists(signin.ID).Result()
			if err != nil {
				app.errorLog.Println(err)
				continue
			}
			if i == 0 {
				_, err := app.rcLogins.Set(signin.ID, 1, (time.Hour * 48)).Result()
				if err != nil {
					app.errorLog.Println(err)
					continue
				}
				msg, err := app.NewGelfLog(signin)
				if err != nil {
					app.errorLog.Println(err)
					continue
				}
				g.Log(string(msg))
			}
		}
	}

}

func (app *application) NewGelfLog(s msgraph.Signin) ([]byte, error) {
	gi := GELFInstance{}
	gi.Timestamp = s.CreatedDateTime.Unix()
	hn, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	gi.AppName = app.config.AppNameInGraylog
	gi.Host = hn
	gi.DeviceDetail = s.DeviceDetail.OperatingSystem + " " + s.DeviceDetail.Browser
	gi.ID = s.ID
	gi.UserDisplayName = s.UserDisplayName
	gi.UserPrincipalName = s.UserPrincipalName
	gi.AppDisplayName = s.AppDisplayName

	if s.Location.City != "" {
		gi.IpAddress = s.IpAddress
		gi.Location = strings.ToLower(s.Location.City + " " + s.Location.State + " " + s.Location.CountryOrRegion)
		gi.LocationCity = strings.ToLower(s.Location.City)
		gi.LocationState = strings.ToLower(s.Location.State)
		gi.LocationCountry = strings.ToLower(s.Location.CountryOrRegion)
		gi.GeoData = strconv.FormatFloat(s.Location.GeoCoordinates.Latitude, 'f', 6, 64) + "," + strconv.FormatFloat(s.Location.GeoCoordinates.Longitude, 'f', 6, 64)
		gi.ShortMessage = s.UserDisplayName + " from " + gi.Location + " with " + gi.DeviceDetail + " via " + s.ResourceDisplayName
	} else {
		d, err := models.Ipv6LookUp(s.IpAddress, app.rcIpLookUp)
		if err != nil {
			return nil, err
		}
		gi.IpAddress = s.IpAddress
		gi.Location = strings.ToLower(d.Data.Geo.City + " " + d.Data.Geo.RegionName + " " + d.Data.Geo.CountryCode)
		gi.LocationCity = strings.ToLower(d.Data.Geo.City)
		gi.LocationState = strings.ToLower(d.Data.Geo.RegionName)
		gi.LocationCountry = strings.ToLower(d.Data.Geo.CountryCode)
		gi.GeoData = strconv.FormatFloat(d.Data.Geo.Latitude, 'f', 6, 64) + "," + strconv.FormatFloat(d.Data.Geo.Longitude, 'f', 6, 64)
		gi.ShortMessage = s.UserDisplayName + " from " + gi.Location + " with " + gi.DeviceDetail + " via " + s.ResourceDisplayName
	}
	abuseipdb, err := models.GetAbuseIPDBData(s.IpAddress, app.config.AbuseIPDBApiKey, app.rcAbuseDB)
	if err != nil {
		return nil, err
	}
	gi.AbuseConfidenceScore = abuseipdb.AbuseConfidenceScore
	gi.TotalReports = abuseipdb.TotalReports
	gi.Isp = abuseipdb.Isp
	gi.StatusCode = s.Status.ErrorCode
	if s.Status.ErrorCode != 0 {
		gi.StatusDescripton = s.Status.FailureReason + s.Status.AdditionalDetails
	}
	gi.ClientAppUsed = s.ClientAppUsed
	gi.ResourceDisplayName = s.ResourceDisplayName

	message, err := json.Marshal(gi)
	if err != nil {
		return nil, err
	}
	return message, nil
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
