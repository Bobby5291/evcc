package graphql

import "time"

// krakenTokenAuthentication is used to obtain a Kraken API token via email/password.
type krakenTokenAuthentication struct {
	ObtainKrakenToken struct {
		Token string
	} `graphql:"obtainKrakenToken(input: {email: $email, password: $password})"`
}

// getElectricityInfo fetches the active electricity agreement, MPAN and tariff codes.
type getElectricityInfo struct {
	Account struct {
		ElectricityAgreements []struct {
			MeterPoint struct {
				Mpan       string
				Agreements []struct {
					ValidFrom time.Time
					ValidTo   time.Time
					Tariff    struct {
						T1 TariffCodes `graphql:"... on TariffType"`
						T2 TariffCodes `graphql:"... on HalfHourlyTariff"`
						T3 TariffCodes `graphql:"... on DayNightTariff"`
						T4 TariffCodes `graphql:"... on FourRateEvTariff"`
					}
				} `graphql:"agreements(includeInactive: false)"`
			}
		} `graphql:"electricityAgreements(active: true)"`
	} `graphql:"account(accountNumber: $accountNumber)"`
}

// TariffCodes holds the product and tariff code for a rate lookup.
type TariffCodes struct {
	ProductCode string
	TariffCode  string
}

// ElectricityInfo is the resolved meter point and tariff details.
type ElectricityInfo struct {
	Mpan        string
	ProductCode string
	TariffCode  string
}

// UnitRate is a single rate period from the REST API.
type UnitRate struct {
	ValueIncVat float64 `json:"value_inc_vat"`
	ValidFrom   string  `json:"valid_from"`
	ValidTo     string  `json:"valid_to"`
}

// UnitRatesResponse is the paginated REST response.
type UnitRatesResponse struct {
	Results []UnitRate `json:"results"`
	Next    string     `json:"next"`
}
