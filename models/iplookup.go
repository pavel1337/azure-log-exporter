package models

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/go-redis/redis"
)

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

func Ipv6LookUp(ip string, rc *redis.Client) (IPLookUpData, error) {
	data := IPLookUpData{}

	dur := 168 * time.Hour
	i, err := rc.Exists(ip).Result()
	if err != nil {
		return data, err
	}
	var keycdngeodata []byte
	if i == 0 {
		keycdngeodata, err = Ipv6LookUpFromApi(ip)
		if err != nil {
			return data, err
		}
		err = json.Unmarshal(keycdngeodata, &data)
		if err != nil {
			return data, err
		}
		_, err := rc.Set(ip, keycdngeodata, dur).Result()
		if err != nil {
			return data, err
		}
	} else {
		keycdngeodata, err = rc.Get(ip).Bytes()
		if err != nil {
			return data, err
		}
		err = json.Unmarshal(keycdngeodata, &data)
		if err != nil {
			return data, err
		}
	}
	return data, nil
}

func Ipv6LookUpFromApi(ip string) ([]byte, error) {
	c := http.Client{
		Timeout: time.Second * 10, // Maximum of 10 secs
	}
	url := "https://tools.keycdn.com/geo.json?host=" + ip
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		err = fmt.Errorf("client error: %v\n", err)
		return nil, err
	}

	res, err := c.Do(req)
	if err != nil {
		err = fmt.Errorf("request error: %v\n", err)
		return nil, err
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		err = fmt.Errorf("response error: %v\n", err)
		return nil, err
	}
	return body, nil
}
