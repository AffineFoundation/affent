package metrictext

import "testing"

func TestHasMultiplePriceLikeValues(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "single value",
			text: "Price 0.06342 T",
			want: false,
		},
		{
			name: "two values across lines",
			text: "TAO Price $277.32\nPrice 0.06342 T",
			want: true,
		},
		{
			name: "duplicate line only once",
			text: "Price 0.06342 T\nPrice 0.06342 T",
			want: false,
		},
		{
			name: "market cap and price",
			text: "TAO Price $277.32\nMarket Cap 201.04K T",
			want: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasMultiplePriceLikeValues(c.text); got != c.want {
				t.Fatalf("HasMultiplePriceLikeValues(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
