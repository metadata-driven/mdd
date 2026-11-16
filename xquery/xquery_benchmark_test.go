package xquery

import "testing"

func BenchmarkParse(b *testing.B) {
	const src = `
		# Orders query
		from(billing/orders)
		select(
			icon,
			ts,
			status,
			user.name,
			amount,
		)
		where( status = done, canceled )
		where( user.type != [ "guest", "moderator" ] )
		where( amount > 123.45 )
		where( description ~ "Отменён по запросу клиента" )
		order(
			user.type asc,
			ts desc,
		)
		limit(100)
		offset(200)
	`

	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	b.ResetTimer()

	for b.Loop() {
		_, err := Parse(src)
		if err != nil {
			b.Fatal(err)
		}
	}
}
