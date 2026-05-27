package main

import (
	"net/http"
	"net/url"
	"strings"
)

func mergeLoginCookiesFromHTTPCookies(dst *loginCookies, cookies []*http.Cookie) {
	if dst == nil {
		return
	}
	for _, c := range cookies {
		switch strings.ToLower(c.Name) {
		case "sessdata":
			if dst.SESSDATA == "" {
				dst.SESSDATA = c.Value
			}
		case "bili_jct":
			if dst.BILI_JCT == "" {
				dst.BILI_JCT = c.Value
			}
		case "buvid3":
			if dst.BUVID3 == "" {
				dst.BUVID3 = c.Value
			}
		case "dedeuserid":
			if dst.DedeUserID == "" {
				dst.DedeUserID = c.Value
			}
		}
	}
}

func extractLoginCookiesFromJar(jar http.CookieJar) loginCookies {
	var out loginCookies
	if jar == nil {
		return out
	}

	urls := []string{
		"https://www.bilibili.com/",
		"https://bilibili.com/",
		"https://passport.bilibili.com/",
		"https://show.bilibili.com/",
	}

	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		for _, c := range jar.Cookies(u) {
			switch strings.ToLower(c.Name) {
			case "sessdata":
				if out.SESSDATA == "" {
					out.SESSDATA = c.Value
				}
			case "bili_jct":
				if out.BILI_JCT == "" {
					out.BILI_JCT = c.Value
				}
			case "buvid3":
				if out.BUVID3 == "" {
					out.BUVID3 = c.Value
				}
			case "dedeuserid":
				if out.DedeUserID == "" {
					out.DedeUserID = c.Value
				}
			}
		}
	}

	return out
}
