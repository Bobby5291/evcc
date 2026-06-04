package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/request"
	"github.com/evcc-io/evcc/util/transport"
	"github.com/hasura/go-graphql-client"
	"golang.org/x/oauth2"
)

const (
	BaseURI    = "https://api.edfgb-kraken.energy/v1/graphql/"
	RestBaseURI = "https://api.edfgb-kraken.energy/v1"
)

// EdfGbGraphQLClient communicates with EDF UK's Kraken platform.
type EdfGbGraphQLClient struct {
	log           *util.Logger
	*graphql.Client
	httpClient    *http.Client
	accountNumber string
}

// NewClient returns a new, authenticated EdfGbGraphQLClient.
func NewClient(log *util.Logger, email, password, accountNumber string) (*EdfGbGraphQLClient, error) {
	ts := oauth2.ReuseTokenSource(nil, &tokenSource{
		log:      log,
		email:    email,
		password: password,
	})

	cli := request.NewClient(log)
	cli.Transport = &transport.Decorator{
		Decorator: func(req *http.Request) error {
			token, err := ts.Token()
			if err != nil {
				return err
			}
			// Kraken API requires Authorization header without "Bearer" prefix
			req.Header.Set("Authorization", token.AccessToken)
			return nil
		},
		Base: cli.Transport,
	}

	gq := &EdfGbGraphQLClient{
		log:           log,
		accountNumber: accountNumber,
		httpClient:    cli,
		Client:        graphql.NewClient(BaseURI, cli),
	}

	return gq, nil
}

// ElectricityInfo fetches the active electricity agreement info (MPAN, product code, tariff code).
func (c *EdfGbGraphQLClient) ElectricityInfo() (ElectricityInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	var q getElectricityInfo
	if err := c.Client.Query(ctx, &q, map[string]any{
		"accountNumber": c.accountNumber,
	}); err != nil {
		return ElectricityInfo{}, err
	}

	for _, agr := range q.Account.ElectricityAgreements {
		mp := agr.MeterPoint
		if mp.Mpan == "" {
			continue
		}
		for _, a := range mp.Agreements {
			// Try each inline fragment type for the tariff codes.
			codes := []TariffCodes{a.Tariff.T1, a.Tariff.T2, a.Tariff.T3, a.Tariff.T4}
			for _, tc := range codes {
				if tc.ProductCode != "" && tc.TariffCode != "" {
					return ElectricityInfo{
						Mpan:        mp.Mpan,
						ProductCode: tc.ProductCode,
						TariffCode:  tc.TariffCode,
					}, nil
				}
			}
		}
	}

	return ElectricityInfo{}, errors.New("no active electricity agreement with tariff codes found")
}

// UnitRates fetches standard unit rates from the REST API for the given time window.
func (c *EdfGbGraphQLClient) UnitRates(productCode, tariffCode string, from, to time.Time) ([]UnitRate, error) {
	url := fmt.Sprintf(
		"%s/products/%s/electricity-tariffs/%s/standard-unit-rates/?period_from=%s&period_to=%s",
		RestBaseURI,
		productCode,
		tariffCode,
		from.UTC().Format(time.RFC3339),
		to.UTC().Format(time.RFC3339),
	)

	var all []UnitRate
	for url != "" {
		rates, next, err := c.fetchRatePage(url)
		if err != nil {
			return nil, err
		}
		all = append(all, rates...)
		url = next
	}
	return all, nil
}

func (c *EdfGbGraphQLClient) fetchRatePage(url string) ([]UnitRate, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("rate API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	var result UnitRatesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", err
	}

	return result.Results, result.Next, nil
}
