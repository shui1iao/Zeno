package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultExchangeRateURL        = "https://www.google.com/finance/quote/{currency}-CNY?hl=en"
	googleFinanceMaxResponseBytes = 2 << 20
	googleFinanceBrowserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/144 Safari/537.36"
)

var exchangeRateCurrencies = []string{"CNY", "USD", "HKD", "EUR", "GBP", "JPY", "SGD", "AUD", "CAD", "KRW"}

var googleFinanceRatePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<div[^>]*class="[^"]*\bN6SYTe\b[^"]*"[^>]*>\s*<span[^>]*>\s*<span[^>]*>\s*([0-9][0-9.,]*)\s*</span>`),
	regexp.MustCompile(`(?s)<div[^>]*class="[^"]*\bYMlKec\b[^"]*"[^>]*>\s*([0-9][0-9.,]*)\s*</div>`),
	regexp.MustCompile(`data-last-price="([0-9][0-9.,]*)"`),
}

type exchangeRateRefreshStore interface {
	RefreshExchangeRates(context.Context, *http.Client, string) error
}

func (s *sqliteMonitoringDomain) RefreshExchangeRates(ctx context.Context, client *http.Client, endpoint string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("exchange rate store is closed")
	}
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultExchangeRateURL
	}
	if !strings.Contains(endpoint, "{currency}") {
		return fmt.Errorf("google finance exchange rate endpoint must contain {currency}")
	}

	type quoteResult struct {
		currency string
		rate     float64
		err      error
	}
	resultCount := len(exchangeRateCurrencies) - 1
	results := make(chan quoteResult, resultCount)
	for _, currency := range exchangeRateCurrencies {
		if currency == "CNY" {
			continue
		}
		go func(currency string) {
			rate, err := fetchGoogleFinanceRate(ctx, client, endpoint, currency)
			results <- quoteResult{currency: currency, rate: rate, err: err}
		}(currency)
	}

	cnyRates := make(map[string]float64, len(exchangeRateCurrencies))
	cnyRates["CNY"] = 1
	for range resultCount {
		result := <-results
		if result.err != nil {
			return fmt.Errorf("fetch Google Finance %s/CNY rate: %w", result.currency, result.err)
		}
		cnyRates[result.currency] = result.rate
	}
	sourceDate := time.Now().In(time.Local).Format("2006-01-02")

	return s.writes.withAgentWrite(ctx, "_exchange_rates", func(writeCtx context.Context) error {
		tx, err := s.db.BeginTx(writeCtx, nil)
		if err != nil {
			return err
		}
		defer func() { rollbackUnlessCommitted(tx) }()
		now := time.Now().UTC().Unix()
		for _, currency := range exchangeRateCurrencies {
			if _, err := tx.ExecContext(writeCtx, `
				INSERT INTO exchange_rates (currency, cny_rate, source_date, updated_at)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(currency) DO UPDATE SET
					cny_rate = excluded.cny_rate,
					source_date = excluded.source_date,
					updated_at = excluded.updated_at
			`, currency, cnyRates[currency], sourceDate, now); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		return nil
	})
}

func fetchGoogleFinanceRate(ctx context.Context, client *http.Client, endpoint, currency string) (float64, error) {
	quoteURL := strings.ReplaceAll(endpoint, "{currency}", currency)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, quoteURL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("User-Agent", googleFinanceBrowserUserAgent)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return 0, fmt.Errorf("google finance returned %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, googleFinanceMaxResponseBytes+1))
	if err != nil {
		return 0, fmt.Errorf("read Google Finance response: %w", err)
	}
	if len(body) > googleFinanceMaxResponseBytes {
		return 0, fmt.Errorf("google finance response is too large")
	}
	return parseGoogleFinanceRate(body)
}

func parseGoogleFinanceRate(body []byte) (float64, error) {
	for _, pattern := range googleFinanceRatePatterns {
		match := pattern.FindSubmatch(body)
		if len(match) != 2 {
			continue
		}
		raw := strings.ReplaceAll(strings.TrimSpace(string(match[1])), ",", "")
		rate, err := strconv.ParseFloat(raw, 64)
		if err == nil && validExchangeRate(rate) {
			return rate, nil
		}
	}
	return 0, fmt.Errorf("google finance response does not contain a valid exchange rate")
}

func validExchangeRate(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (s *sqliteMonitoringDomain) exchangeRateSnapshot(ctx context.Context) (map[string]float64, error) {
	readDB := s.readDB
	if readDB == nil {
		readDB = s.db
	}
	rates := map[string]float64{"CNY": 1}
	rows, err := readDB.QueryContext(ctx, `
		SELECT currency, cny_rate
		FROM exchange_rates
		ORDER BY currency ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var currency string
		var rate float64
		if err := rows.Scan(&currency, &rate); err != nil {
			return nil, err
		}
		currency = strings.ToUpper(strings.TrimSpace(currency))
		if currency == "" || !validExchangeRate(rate) {
			continue
		}
		rates[currency] = rate
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rates["CNY"] = 1
	return rates, nil
}

func (h *handler) runExchangeRateRefresher(ctx context.Context, interval time.Duration, client *http.Client, endpoint string) {
	store, ok := h.store.(exchangeRateRefreshStore)
	if !ok || interval <= 0 {
		return
	}
	refresh := func() {
		refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := store.RefreshExchangeRates(refreshCtx, client, endpoint); err != nil {
			if refreshCtx.Err() == nil && ctx.Err() == nil {
				log.Printf("exchange rate refresh failed: %v", err)
			}
			return
		}
		h.markSummaryCacheDirty()
		h.publishSummary(ctx)
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
