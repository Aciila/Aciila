package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const graphqlURL = "https://api.github.com/graphql"

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphQLResponse struct {
	Data struct {
		User struct {
			ContributionsCollection struct {
				ContributionCalendar struct {
					Weeks []struct {
						ContributionDays []struct {
							Date              string `json:"date"`
							ContributionCount int    `json:"contributionCount"`
						} `json:"contributionDays"`
					} `json:"weeks"`
					TotalContributions int `json:"totalContributions"`
				} `json:"contributionCalendar"`
			} `json:"contributionsCollection"`
			Repositories struct {
				Nodes []struct {
					Languages struct {
						Edges []struct {
							Size int `json:"size"`
							Node struct {
								Name string `json:"name"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"languages"`
				} `json:"nodes"`
			} `json:"repositories"`
		} `json:"user"`
	} `json:"data"`
	Errors []any `json:"errors"`
}

type contributionDay struct {
	Date  string
	Count int
}

type languageSize struct {
	Name string
	Size int
}

func githubGraphQL(token, query string, variables map[string]any) (*graphQLResponse, error) {
	payload, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, graphqlURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "profile-visual-generator-go")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("graphql request failed with status %s", resp.Status)
	}

	var out graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		return nil, fmt.Errorf("graphql returned errors: %v", out.Errors)
	}

	return &out, nil
}

func fetchContributionRange(user, token string, fromDate, toDate time.Time) ([]contributionDay, int, error) {
	query := `
query($login: String!, $from: DateTime!, $to: DateTime!) {
  user(login: $login) {
    contributionsCollection(from: $from, to: $to) {
      contributionCalendar {
        weeks {
          contributionDays {
            date
            contributionCount
          }
        }
        totalContributions
      }
    }
  }
}
`

	data, err := githubGraphQL(token, query, map[string]any{
		"login": user,
		"from": fromDate.Format(time.RFC3339),
		"to":   toDate.Format(time.RFC3339),
	})
	if err != nil {
		return nil, 0, err
	}

	days := make([]contributionDay, 0, 400)
	for _, week := range data.Data.User.ContributionsCollection.ContributionCalendar.Weeks {
		for _, d := range week.ContributionDays {
			days = append(days, contributionDay{Date: d.Date, Count: d.ContributionCount})
		}
	}

	total := data.Data.User.ContributionsCollection.ContributionCalendar.TotalContributions
	return days, total, nil
}

func fetchProfileData(user, token string) ([]contributionDay, map[string]int, int, error) {
	toDate := time.Now().UTC().Truncate(time.Second)
	recentFrom := toDate.AddDate(0, 0, -364)
	olderTo := recentFrom.Add(-time.Second)
	olderFrom := olderTo.AddDate(0, 0, -364)

	olderDays, olderTotal, err := fetchContributionRange(user, token, olderFrom, olderTo)
	if err != nil {
		return nil, nil, 0, err
	}
	recentDays, recentTotal, err := fetchContributionRange(user, token, recentFrom, toDate)
	if err != nil {
		return nil, nil, 0, err
	}

	days := make([]contributionDay, 0, len(olderDays)+len(recentDays))
	days = append(days, olderDays...)
	days = append(days, recentDays...)

	total := olderTotal + recentTotal

	langQuery := `
query($login: String!) {
  user(login: $login) {
    repositories(
      first: 100,
      ownerAffiliations: OWNER,
      isFork: false,
      orderBy: {field: UPDATED_AT, direction: DESC}
    ) {
      nodes {
        languages(first: 10, orderBy: {field: SIZE, direction: DESC}) {
          edges {
            size
            node {
              name
            }
          }
        }
      }
    }
  }
}
`

	langData, err := githubGraphQL(token, langQuery, map[string]any{
		"login": user,
	})
	if err != nil {
		return nil, nil, 0, err
	}

	langSizes := map[string]int{}
	for _, repo := range langData.Data.User.Repositories.Nodes {
		for _, edge := range repo.Languages.Edges {
			langSizes[edge.Node.Name] += edge.Size
		}
	}

	return days, langSizes, total, nil
}

func generateCommitSkyline(days []contributionDay, total int, username string) string {
	const bucketSize = 14
	buckets := make([]int, 0, 60)
	for i := 0; i < len(days); i += bucketSize {
		end := i + bucketSize
		if end > len(days) {
			end = len(days)
		}
		sum := 0
		for _, d := range days[i:end] {
			sum += d.Count
		}
		buckets = append(buckets, sum)
	}

	if len(buckets) > 52 {
		buckets = buckets[len(buckets)-52:]
	}

	maxValue := 1
	for _, v := range buckets {
		if v > maxValue {
			maxValue = v
		}
	}

	const (
		width  = 960
		height = 320
		baseY  = 250
		barW   = 12
		gap    = 5
		startX = 36
	)

	var bars strings.Builder
	var windows strings.Builder

	for idx, value := range buckets {
		h := 30 + int(float64(value)/float64(maxValue)*170.0)
		x := startX + idx*(barW+gap)
		y := baseY - h
		delay := float64(idx) * 0.04

		bars.WriteString(fmt.Sprintf(
			"<rect class=\"building\" x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\" style=\"animation-delay:%.2fs\" />",
			x, y, barW, h, delay,
		))

		winRows := h / 20
		if winRows < 1 {
			winRows = 1
		}
		for r := 0; r < winRows; r++ {
			wx := x + 2
			wy := baseY - (r+1)*16
			if wy <= y+2 {
				break
			}
			windows.WriteString(fmt.Sprintf(
				"<rect class=\"window\" x=\"%d\" y=\"%d\" width=\"3\" height=\"5\" style=\"animation-delay:%.2fs\" />",
				wx, wy, float64(idx+r)*0.07,
			))
			windows.WriteString(fmt.Sprintf(
				"<rect class=\"window\" x=\"%d\" y=\"%d\" width=\"3\" height=\"5\" style=\"animation-delay:%.2fs\" />",
				wx+6, wy, float64(idx+r)*0.08,
			))
		}
	}

	return fmt.Sprintf(`<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Commit skyline">
  <defs>
    <linearGradient id="sky" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%%" stop-color="#07152d"/>
      <stop offset="100%%" stop-color="#0f2e5c"/>
    </linearGradient>
    <linearGradient id="bld" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%%" stop-color="#51a2ff"/>
      <stop offset="100%%" stop-color="#2668c6"/>
    </linearGradient>
    <style>
      .title { font: 700 24px monospace; fill: #dce9ff; }
      .sub { font: 500 13px monospace; fill: #98b8ea; }
      .building { fill: url(#bld); transform-origin: center 250px; opacity: 0; animation: rise .7s ease forwards; }
      .window { fill: #ffd86b; opacity: 0; animation: blink 2.4s ease-in-out infinite; }
      @keyframes rise { from { transform: scaleY(0.05); opacity: 0; } to { transform: scaleY(1); opacity: 1; } }
      @keyframes blink { 0%%, 35%%, 100%% { opacity: .2; } 50%% { opacity: 1; } }
    </style>
  </defs>
  <rect width="100%%" height="100%%" fill="url(#sky)"/>
  <text x="36" y="44" class="title">Commit Skyline</text>
  <text x="36" y="66" class="sub">@%s • %d contributions in the last 2 years</text>
  <rect x="0" y="250" width="100%%" height="70" fill="#071224"/>
  %s
  %s
</svg>
`, width, height, username, total, bars.String(), windows.String())
}

func generateTechOrbit(languageSizes map[string]int, username string) string {
	filtered := map[string]int{}
	for name, size := range languageSizes {
		lower := strings.ToLower(name)
		if lower == "python" || lower == "php" {
			continue
		}
		filtered[name] = size
	}

	if _, ok := filtered["Go"]; !ok {
		filtered["Go"] = 1
	}

	langs := make([]languageSize, 0, len(filtered))
	for name, size := range filtered {
		langs = append(langs, languageSize{Name: name, Size: size})
	}
	sort.Slice(langs, func(i, j int) bool {
		if langs[i].Size == langs[j].Size {
			return langs[i].Name < langs[j].Name
		}
		return langs[i].Size > langs[j].Size
	})

	if len(langs) > 8 {
		langs = langs[:8]
	}

	goInTop := false
	for _, l := range langs {
		if l.Name == "Go" {
			goInTop = true
			break
		}
	}
	if !goInTop {
		if len(langs) == 8 {
			langs[7] = languageSize{Name: "Go", Size: 1}
		} else {
			langs = append(langs, languageSize{Name: "Go", Size: 1})
		}
	}

	if len(langs) == 0 {
		langs = []languageSize{
			{Name: "TypeScript", Size: 10},
			{Name: "JavaScript", Size: 9},
			{Name: "Solidity", Size: 8},
			{Name: "Go", Size: 7},
		}
	}

	palette := []string{"#61dafb", "#f7df1e", "#3178c6", "#9cdbff", "#00d8ff", "#c678dd", "#ff9e64", "#5fd3bc"}
	total := 0
	for _, l := range langs {
		total += l.Size
	}
	if total == 0 {
		total = 1
	}

	cx, cy := 480.0, 190.0
	var circles strings.Builder
	var labels strings.Builder

	for i, l := range langs {
		angle := (2 * math.Pi / float64(len(langs))) * float64(i)
		radius := 105.0 + float64(i%3)*24.0
		x := cx + math.Cos(angle)*(180.0+float64(i%2)*28.0)
		y := cy + math.Sin(angle)*(82.0+float64(i%3)*14.0)
		dotR := 7 + int(float64(l.Size)/float64(total)*28.0)
		color := palette[i%len(palette)]
		delay := float64(i) * 0.23

		circles.WriteString(fmt.Sprintf(
			"<circle class=\"orb\" cx=\"%.1f\" cy=\"%.1f\" r=\"%d\" fill=\"%s\" style=\"animation-delay:%.2fs\"/>",
			x, y, dotR, color, delay,
		))
		labels.WriteString(fmt.Sprintf(
			"<text class=\"label\" x=\"%.1f\" y=\"%.1f\" text-anchor=\"middle\">%s</text>",
			x, y+float64(dotR)+15.0, l.Name,
		))
		circles.WriteString(fmt.Sprintf(
			"<ellipse class=\"path\" cx=\"%.0f\" cy=\"%.0f\" rx=\"%.1f\" ry=\"%.1f\" transform=\"rotate(%d %.0f %.0f)\"/>",
			cx, cy, radius, radius*0.45, i*22, cx, cy,
		))
	}

	return fmt.Sprintf(`<svg viewBox="0 0 960 360" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Tech orbit">
  <defs>
    <radialGradient id="bg" cx="50%%" cy="50%%" r="70%%">
      <stop offset="0%%" stop-color="#141c33"/>
      <stop offset="100%%" stop-color="#080d1c"/>
    </radialGradient>
    <style>
      .title { font: 700 24px monospace; fill: #e6efff; }
      .sub { font: 500 13px monospace; fill: #9cb2df; }
      .core { fill: #1f4a8a; stroke: #8ebeff; stroke-width: 2; animation: pulse 3s ease-in-out infinite; }
      .path { fill: none; stroke: #2b3f65; stroke-width: 1.1; opacity: .7; }
      .orb { filter: drop-shadow(0 0 6px rgba(124,189,255,.4)); transform-origin: 480px 190px; animation: orbit 13s linear infinite; }
      .label { font: 500 11px monospace; fill: #bfd4ff; }
      @keyframes orbit { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
      @keyframes pulse { 0%%,100%% { r: 44; } 50%% { r: 50; } }
    </style>
  </defs>
  <rect width="100%%" height="100%%" fill="url(#bg)"/>
  <text x="36" y="44" class="title">Tech Orbit</text>
  <text x="36" y="66" class="sub">Top languages from owned repositories • @%s</text>
  %s
  <circle class="core" cx="480" cy="190" r="44"/>
  <text x="480" y="195" text-anchor="middle" style="font:700 12px monospace; fill:#d9ebff;">CORE</text>
  %s
</svg>
`, username, circles.String(), labels.String())
}

func generateActivityPulse(days []contributionDay, username string) string {
	recent := days
	if len(days) >= 90 {
		recent = days[len(days)-90:]
	}
	values := make([]int, 0, len(recent))
	for _, d := range recent {
		values = append(values, d.Count)
	}
	if len(values) == 0 {
		values = []int{0}
	}

	maxValue := 1
	for _, v := range values {
		if v > maxValue {
			maxValue = v
		}
	}

	const (
		width  = 960.0
		height = 280.0
		left   = 60.0
		top    = 70.0
		chartW = 860.0
		chartH = 150.0
	)

	points := make([]string, 0, len(values))
	var bars strings.Builder

	for i, v := range values {
		x := left + (float64(i)/math.Max(float64(len(values)-1), 1.0))*chartW
		y := top + chartH - (float64(v)/float64(maxValue))*(chartH-8.0)
		points = append(points, fmt.Sprintf("%.1f,%.1f", x, y))

		bh := math.Max(2.0, (float64(v)/float64(maxValue))*46.0)
		bx := left + float64(i)*(chartW/math.Max(float64(len(values)), 1.0))
		by := top + chartH + 8.0
		bars.WriteString(fmt.Sprintf(
			"<rect class=\"beat\" x=\"%.1f\" y=\"%.1f\" width=\"4\" height=\"%.1f\" style=\"animation-delay:%.2fs\"/>",
			bx, by, bh, float64(i)*0.03,
		))
	}

	sum := 0
	for _, v := range values {
		sum += v
	}
	avg := float64(sum) / float64(len(values))

	streak := 0
	maxStreak := 0
	for _, v := range values {
		if v > 0 {
			streak++
			if streak > maxStreak {
				maxStreak = streak
			}
		} else {
			streak = 0
		}
	}

	return fmt.Sprintf(`<svg viewBox="0 0 960 280" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Activity pulse">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%%" stop-color="#111827"/>
      <stop offset="100%%" stop-color="#0b1220"/>
    </linearGradient>
    <linearGradient id="line" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%%" stop-color="#31c7ff"/>
      <stop offset="100%%" stop-color="#7af59a"/>
    </linearGradient>
    <style>
      .title { font: 700 24px monospace; fill: #e9f2ff; }
      .sub { font: 500 13px monospace; fill: #9cb0d2; }
      .grid { stroke: #22304d; stroke-width: 1; opacity: .5; }
      .line { fill: none; stroke: url(#line); stroke-width: 3; stroke-linecap: round; stroke-linejoin: round; stroke-dasharray: 1600; stroke-dashoffset: 1600; animation: draw 2.2s ease forwards; }
      .beat { fill: #55e3ff; opacity: .25; transform-origin: center; animation: pulse 1.6s ease-in-out infinite; }
      @keyframes draw { to { stroke-dashoffset: 0; } }
      @keyframes pulse { 0%%,100%% { opacity: .2; } 50%% { opacity: .75; } }
    </style>
  </defs>
  <rect width="100%%" height="100%%" fill="url(#bg)"/>
  <text x="36" y="42" class="title">Activity Pulse Timeline</text>
  <text x="36" y="63" class="sub">Last 90 days • avg/day %.1f • best streak %d days • @%s</text>
  <g>
    <line class="grid" x1="60" y1="220" x2="920" y2="220"/>
    <line class="grid" x1="60" y1="170" x2="920" y2="170"/>
    <line class="grid" x1="60" y1="120" x2="920" y2="120"/>
    <line class="grid" x1="60" y1="70" x2="920" y2="70"/>
  </g>
  <polyline class="line" points="%s"/>
  %s
</svg>
`, avg, maxStreak, username, strings.Join(points, " "), bars.String())
}

func main() {
	token := os.Getenv("GITHUB_TOKEN")
	user := os.Getenv("PROFILE_USERNAME")
	if user == "" {
		user = os.Getenv("GITHUB_REPOSITORY_OWNER")
	}

	if token == "" {
		panic("GITHUB_TOKEN is required")
	}
	if user == "" {
		panic("PROFILE_USERNAME or GITHUB_REPOSITORY_OWNER is required")
	}

	days, languageSizes, total, err := fetchProfileData(user, token)
	if err != nil {
		panic(err)
	}

	if err := os.MkdirAll("dist", 0o755); err != nil {
		panic(err)
	}

	files := map[string]string{
		"commit-skyline.svg": generateCommitSkyline(days, total, user),
		"tech-orbit.svg":     generateTechOrbit(languageSizes, user),
		"activity-pulse.svg": generateActivityPulse(days, user),
	}

	for name, content := range files {
		path := filepath.Join("dist", name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			panic(err)
		}
	}
}
