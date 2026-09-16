package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubGlobalPricingRepo struct {
	rows []GlobalModelPrice
}

func (s *stubGlobalPricingRepo) ListEnabled(context.Context) ([]GlobalModelPrice, error) {
	return append([]GlobalModelPrice(nil), s.rows...), nil
}

func (s *stubGlobalPricingRepo) ListAll(context.Context) ([]GlobalModelPrice, error) {
	return append([]GlobalModelPrice(nil), s.rows...), nil
}

func (s *stubGlobalPricingRepo) Create(_ context.Context, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	out := GlobalModelPrice{ID: 1, ModelPattern: in.ModelPattern, BillingMode: in.BillingMode,
		InputPrice: in.InputPrice, OutputPrice: in.OutputPrice, Enabled: in.Enabled}
	s.rows = append(s.rows, out)
	return &out, nil
}

func (s *stubGlobalPricingRepo) GetByID(_ context.Context, id int64) (*GlobalModelPrice, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			cp := s.rows[i]
			return &cp, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *stubGlobalPricingRepo) Update(_ context.Context, id int64, in GlobalModelPriceInput) (*GlobalModelPrice, error) {
	return &GlobalModelPrice{ID: id, ModelPattern: in.ModelPattern}, nil
}

func (s *stubGlobalPricingRepo) Delete(context.Context, int64) error { return nil }

func (s *stubGlobalPricingRepo) SetEnabled(_ context.Context, id int64, enabled bool) error {
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Enabled = enabled
			return nil
		}
	}
	return sql.ErrNoRows
}

func fptr(v float64) *float64 { return &v }

func TestValidateGlobalModelPriceInput(t *testing.T) {
	valid := GlobalModelPriceInput{ModelPattern: "gpt-5", InputPrice: fptr(1), OutputPrice: fptr(2)}
	require.NoError(t, ValidateGlobalModelPriceInput(valid))
	require.NoError(t, ValidateGlobalModelPriceInput(GlobalModelPriceInput{ModelPattern: "gpt-5*", OutputPrice: fptr(2)}))
	require.NoError(t, ValidateGlobalModelPriceInput(GlobalModelPriceInput{
		ModelPattern: "x", BillingMode: BillingModePerRequest, PerRequestPrice: fptr(0.01)}))

	bad := []GlobalModelPriceInput{
		{ModelPattern: ""},
		{ModelPattern: "*"},
		{ModelPattern: "*foo"},
		{ModelPattern: "a*b"},
		{ModelPattern: "has space"},
		{ModelPattern: "x", BillingMode: "hourly"},
		{ModelPattern: "x", InputPrice: fptr(-1)},
		{ModelPattern: "x"},
		{ModelPattern: "x", BillingMode: BillingModePerRequest},
		{ModelPattern: "x", BillingMode: BillingModeVideo, InputPrice: fptr(1)},
	}
	for i, in := range bad {
		require.Error(t, ValidateGlobalModelPriceInput(in), "case %d", i)
	}
}

func TestGlobalModelPricingMatch(t *testing.T) {
	svc := NewGlobalModelPricingService(&stubGlobalPricingRepo{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "gpt-5", BillingMode: BillingModeToken, InputPrice: fptr(1), Enabled: true},
		{ID: 2, ModelPattern: "gpt-*", BillingMode: BillingModeToken, InputPrice: fptr(5), Enabled: true},
		{ID: 3, ModelPattern: "gpt-5-*", BillingMode: BillingModeToken, InputPrice: fptr(7), Enabled: true},
	}})

	// 精确优先于通配
	hit := svc.Match(context.Background(), "GPT-5")
	require.NotNil(t, hit)
	require.Equal(t, 1.0, *hit.InputPrice)

	// 最长前缀通配
	hit = svc.Match(context.Background(), "gpt-5-turbo")
	require.NotNil(t, hit)
	require.Equal(t, 7.0, *hit.InputPrice)

	hit = svc.Match(context.Background(), "gpt-4o")
	require.NotNil(t, hit)
	require.Equal(t, 5.0, *hit.InputPrice)

	// 未命中
	require.Nil(t, svc.Match(context.Background(), "claude-sonnet-4"))

	// nil 服务恒未命中
	var nilSvc *GlobalModelPricingService
	require.Nil(t, nilSvc.Match(context.Background(), "gpt-5"))
}

func TestModelPricingResolverGlobalFirst(t *testing.T) {
	repo := &stubGlobalPricingRepo{rows: []GlobalModelPrice{
		{ID: 1, ModelPattern: "gpt-5", BillingMode: BillingModeToken,
			InputPrice: fptr(10), OutputPrice: fptr(20), Enabled: true},
	}}
	global := NewGlobalModelPricingService(repo)
	r := NewModelPricingResolverWithGlobal(nil, &BillingService{}, global)

	resolved := r.Resolve(context.Background(), PricingInput{
		Model: "gpt-5",
		Group: &Group{ID: 9, ModelPricing: []ChannelModelPricing{{
			Models: []string{"gpt-5"}, InputPrice: fptr(1), OutputPrice: fptr(1),
		}}},
	})
	require.Equal(t, PricingSourceGlobal, resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	require.Equal(t, 10.0, resolved.BasePricing.InputPricePerToken)
	require.Equal(t, 20.0, resolved.BasePricing.OutputPricePerToken)
}

func TestModelPricingResolverWithoutGlobalKeepsLegacy(t *testing.T) {
	r := NewModelPricingResolver(nil, &BillingService{})
	resolved := r.Resolve(context.Background(), PricingInput{Model: "gpt-5"})
	require.NotEqual(t, PricingSourceGlobal, resolved.Source)
}
