package tariff

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/evcc-io/evcc/api"
	edfGbGql "github.com/evcc-io/evcc/tariff/edfgb/graphql"
	"github.com/evcc-io/evcc/util"
)

type EdfGb struct {
	log       *util.Logger
	gqlClient *edfGbGql.EdfGbGraphQLClient
	data      *util.Monitor[api.Rates]
}

var _ api.Tariff = (*EdfGb)(nil)

func init() {
	registry.Add("edf-gb", NewEdfGbFromConfig)
}

// NewEdfGbFromConfig creates the tariff provider from the given config map, and runs it.
func NewEdfGbFromConfig(other map[string]any) (api.Tariff, error) {
	t, err := buildEdfGbFromConfig(other)
	if err != nil {
		return nil, err
	}
	return runOrError(t)
}

func buildEdfGbFromConfig(other map[string]any) (*EdfGb, error) {
	var cc struct {
		Email         string
		Password      string
		AccountNumber string
	}

	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}

	if cc.Email == "" {
		return nil, errors.New("missing email")
	}
	if cc.Password == "" {
		return nil, errors.New("missing password")
	}
	if cc.AccountNumber == "" {
		return nil, errors.New("missing account number")
	}

	log := util.NewLogger("edf-gb")

	gqlClient, err := edfGbGql.NewClient(log, cc.Email, cc.Password, cc.AccountNumber)
	if err != nil {
		return nil, err
	}

	return &EdfGb{
		log:       log,
		gqlClient: gqlClient,
		data:      util.NewMonitor[api.Rates](2 * time.Hour),
	}, nil
}

func (t *EdfGb) run(done chan error) {
	var once sync.Once

	// Discover tariff info (MPAN, product code, tariff code) once at startup.
	var info edfGbGql.ElectricityInfo
	if err := backoff.Retry(func() error {
		var err error
		info, err = t.gqlClient.ElectricityInfo()
		if err != nil {
			if errors.Is(err, edfGbGql.ErrAuthFailed) {
				return backoff.Permanent(err)
			}
			return backoffPermanentError(err)
		}
		return nil
	}, bo()); err != nil {
		once.Do(func() { done <- fmt.Errorf("failed to discover electricity info: %w", err) })
		return
	}

	t.log.DEBUG.Printf("MPAN: %s, product: %s, tariff: %s", info.Mpan, info.ProductCode, info.TariffCode)

	for tick := time.Tick(time.Hour); ; <-tick {
		now := time.Now()
		from := now
		to := now.AddDate(0, 0, planDays)

		var rawRates []edfGbGql.UnitRate

		if err := backoff.Retry(func() error {
			var err error
			rawRates, err = t.gqlClient.UnitRates(info.ProductCode, info.TariffCode, from, to)
			if err != nil {
				if errors.Is(err, edfGbGql.ErrAuthFailed) {
					return backoff.Permanent(err)
				}
				return backoffPermanentError(err)
			}
			return nil
		}, bo()); err != nil {
			once.Do(func() { done <- err })
			t.log.ERROR.Printf("failed to fetch unit rate forecast: %v", err)
			continue
		}

		t.log.DEBUG.Printf("fetched %d rate periods", len(rawRates))

		data := make(api.Rates, 0, len(rawRates))
		for _, r := range rawRates {
			validFrom, err := time.Parse(time.RFC3339, r.ValidFrom)
			if err != nil {
				t.log.WARN.Printf("failed to parse valid_from %q: %v", r.ValidFrom, err)
				continue
			}
			var validTo time.Time
			if r.ValidTo != "" {
				validTo, err = time.Parse(time.RFC3339, r.ValidTo)
				if err != nil {
					t.log.WARN.Printf("failed to parse valid_to %q: %v", r.ValidTo, err)
					continue
				}
			} else {
				validTo = to
			}
			data = append(data, api.Rate{
				Start: validFrom,
				End:   validTo,
				// value_inc_vat is in pence/kWh; divide by 100 to get £/kWh
				Value: r.ValueIncVat / 100,
			})
		}

		mergeRates(t.data, data)
		once.Do(func() { close(done) })
	}
}

// Rates implements the api.Tariff interface.
func (t *EdfGb) Rates() (api.Rates, error) {
	var res api.Rates
	err := t.data.GetFunc(func(val api.Rates) {
		res = slices.Clone(val)
	})
	return res, err
}

// Type implements the api.Tariff interface.
func (t *EdfGb) Type() api.TariffType {
	return api.TariffTypePriceForecast
}
