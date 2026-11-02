package tools

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
)

// RateResult 一次汇率查询的结果。
type RateResult struct {
	Amount float64            //换算金额
	Base   string             //基准货币 ISO 代码
	Date   string             //汇率数据日期（欧央行工作日）
	Rates  map[string]float64 //目标货币 -> 汇率（已按 amount 折算）
}

// currencyAliases 常见货币中文名/别名 -> ISO 4217 代码。
// 键统一为小写，查询前先 ToLower 再查表；未命中的按原样当作 ISO 代码处理。
var currencyAliases = map[string]string{
	"人民币": "CNY", "元": "CNY", "rmb": "CNY", "yuan": "CNY",
	"美元": "USD", "美金": "USD", "dollar": "USD", "dollars": "USD",
	"日元": "JPY", "日币": "JPY", "yen": "JPY",
	"欧元": "EUR", "euro": "EUR",
	"英镑": "GBP",
	"港币": "HKD", "港元": "HKD",
	"澳门元": "MOP", "澳门币": "MOP",
	"台币": "TWD", "新台币": "TWD",
	"韩元": "KRW", "韩币": "KRW",
	"澳元": "AUD", "澳币": "AUD",
	"加元":   "CAD",
	"瑞士法郎": "CHF", "瑞郎": "CHF",
	"新加坡元": "SGD", "坡币": "SGD",
	"泰铢":   "THB",
	"卢布":   "RUB",
	"印度卢比": "INR", "卢比": "INR",
	"林吉特":   "MYR",
	"越南盾":   "VND",
	"菲律宾比索": "PHP", "比索": "PHP",
	"印尼盾":   "IDR",
	"巴西雷亚尔": "BRL",
	"新西兰元":  "NZD", "纽币": "NZD",
}

// NormalizeCurrency 把单个币种输入（中文名/别名/大小写混乱的 ISO 代码）
// 归一化为大写 ISO 代码；无法识别时原样返回大写形式，由接口侧报错。
func NormalizeCurrency(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if code, ok := currencyAliases[s]; ok {
		return code
	}
	return strings.ToUpper(s)
}

// normalizeTargets 归一化逗号分隔的多个目标币种。
func normalizeTargets(targets string) string {
	parts := strings.Split(targets, ",")
	for i, p := range parts {
		parts[i] = NormalizeCurrency(p)
	}
	return strings.Join(parts, ",")
}

// FetchRates 查询欧央行参考汇率，供 get_exchange_rate 工具与 TUI 前端共用。
// base 与 targets 均支持中文币种名（如 人民币、美元）或 ISO 代码；
// targets 支持逗号分隔多个，date 为空表示最新（仅工作日）。
func FetchRates(ctx context.Context, base, targets string, amount float64, date string) (RateResult, error) {
	base = NormalizeCurrency(base)
	if base == "" {
		return RateResult{}, fmt.Errorf("base_currency 不能为空")
	}

	// date：不传默认 latest
	apiDate := "latest"
	if date = strings.TrimSpace(date); date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return RateResult{}, fmt.Errorf("日期格式错误，应为 YYYY-MM-DD: %w", err)
		}
		apiDate = date
	}

	params := url.Values{}
	params.Set("base", base)
	params.Set("symbols", normalizeTargets(targets))
	if amount != 1 {
		params.Set("amount", fmt.Sprintf("%g", amount))
	}

	apiURL := "https://api.frankfurter.dev/v1/" + apiDate + "?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return RateResult{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return RateResult{}, fmt.Errorf("汇率接口 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Amount float64            `json:"amount"`
		Base   string             `json:"base"`
		Date   string             `json:"date"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return RateResult{}, err
	}
	if len(result.Rates) == 0 {
		return RateResult{}, fmt.Errorf("接口未返回 %s 兑 %s 的汇率数据", base, targets)
	}
	return RateResult{Amount: result.Amount, Base: result.Base, Date: result.Date, Rates: result.Rates}, nil
}

// FormatRateLines 将查询结果格式化为多行文本（工具返回与 TUI 共用）。
func FormatRateLines(r RateResult) []string {
	// 排序保证输出稳定
	currencies := make([]string, 0, len(r.Rates))
	for cur := range r.Rates {
		currencies = append(currencies, cur)
	}
	sort.Strings(currencies)

	lines := []string{fmt.Sprintf("汇率数据日期: %s（%s 兑出金额 %g）", r.Date, r.Base, r.Amount)}
	for _, cur := range currencies {
		rate := r.Rates[cur]
		if r.Amount == 1 {
			lines = append(lines, fmt.Sprintf("1 %s = %g %s", r.Base, rate, cur))
		} else {
			lines = append(lines, fmt.Sprintf("%g %s = %g %s（1 %s = %g %s）",
				r.Amount, r.Base, rate, cur, r.Base, rate/r.Amount, cur))
		}
	}
	return lines
}

func NewGetExchangeRate() *ToolDef {
	return &ToolDef{
		Name:        "get_exchange_rate",
		Description: "查询两种货币之间的汇率并进行金额换算。数据来自欧央行(ECB)参考汇率，支持最新汇率和 1999 年以来的历史汇率（仅工作日，周末/节假日自动取最近一个工作日）。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"base_currency": map[string]string{
					"type":        "string",
					"description": "基准货币的 ISO 4217 代码，例如 CNY、USD、JPY、EUR",
				},
				"target_currency": map[string]string{
					"type":        "string",
					"description": "目标货币的 ISO 4217 代码，例如 JPY、USD。多个货币用英文逗号分隔，例如 USD,JPY",
				},
				"amount": map[string]string{
					"type":        "number",
					"description": "要换算的金额，默认 1（即只查汇率不做金额换算）",
				},
				"date": map[string]string{
					"type":        "string",
					"description": "历史汇率日期，格式 YYYY-MM-DD，例如 2026-01-01。不传则默认最新汇率",
				},
			},
			"required": []string{"base_currency", "target_currency"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			base, _ := args["base_currency"].(string)
			target, _ := args["target_currency"].(string)
			if strings.TrimSpace(base) == "" || strings.TrimSpace(target) == "" {
				return "", fmt.Errorf("base_currency 和 target_currency 不能为空")
			}

			// amount：默认 1
			amount := 1.0
			if a, ok := args["amount"].(float64); ok && a > 0 {
				amount = a
			}

			// date：不传默认 latest
			date, _ := args["date"].(string)

			result, err := FetchRates(ctx, base, target, amount, date)
			if err != nil {
				return "", err
			}
			return strings.Join(FormatRateLines(result), "\n"), nil
		},
	}
}

// newHTTPClient 构建带系统证书池的 HTTP 客户端
// 若工作目录存在 ssl.crt（TLS 拦截者 CA 证书），则追加到信任池
func newHTTPClient() *http.Client {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if caCert, err := os.ReadFile("ssl.crt"); err == nil {
		rootCAs.AppendCertsFromPEM(caCert)
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: rootCAs},
		},
	}
}
