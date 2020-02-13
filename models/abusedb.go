package models

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/go-redis/redis"
)

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

func GetAbuseIPDBData(ip, abusipdbapikey string, rc *redis.Client) (AbuseIPDBData, error) {
	var marsh struct {
		AbuseIPDBData AbuseIPDBData `json:"data"`
	}
	dur := 48 * time.Hour
	i, err := rc.Exists(ip).Result()
	if err != nil {
		return marsh.AbuseIPDBData, err
	}
	var aipdbdata []byte
	if i == 0 {
		aipdbdata, err = GetAbuseIPDBDataFromApi(ip, abusipdbapikey)
		if err != nil {
			return marsh.AbuseIPDBData, err
		}
		err = json.Unmarshal(aipdbdata, &marsh)
		if err != nil {
			return marsh.AbuseIPDBData, err
		}
		_, err := rc.Set(ip, aipdbdata, dur).Result()
		if err != nil {
			return marsh.AbuseIPDBData, err
		}
	} else {
		aipdbdata, err = rc.Get(ip).Bytes()
		if err != nil {
			return marsh.AbuseIPDBData, err
		}
		err = json.Unmarshal(aipdbdata, &marsh)
		if err != nil {
			return marsh.AbuseIPDBData, err
		}
	}
	return marsh.AbuseIPDBData, err
}

func GetAbuseIPDBDataFromApi(ip string, apikey string) ([]byte, error) {
	baseUrl, _ := url.Parse("https://api.abuseipdb.com/api/v2/check")
	params := url.Values{}
	params.Add("ipAddress", ip)
	params.Add("maxAgeInDays", "365")
	baseUrl.RawQuery = params.Encode()

	req, err := http.NewRequest("GET", baseUrl.String(), nil)
	if err != nil {
		err = fmt.Errorf("client error: %v\n", err)
		return nil, err
	}

	req.Header.Set("Key", apikey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		err = fmt.Errorf("request error: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("response error: %v\n", err)
		return nil, err
	}
	return respBody, nil
}
