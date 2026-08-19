## Todo 1 — buffer sized to the spawned count:
collector.go:59 allocates the channel buffered to the full capacity the caller passes:
answers: make(chan Answer[K, T], capacity)   // capacity == len(c.parties)
Both call sites pass len(c.parties) (multisig/spend.go:130, boolpolicy/spend.go:154), while spawning at most counter workers, and counter <= len(c.parties) always (it's only incremented for non-"me" parties). The todo's specific worry — counter being smaller than len(c.parties) when some parties are "me" — is safe in this direction: the buffer is sized to the larger number, so every spawned sender has a slot. Send (collector.go:66) can never block.

I confirmed each worker calls Send exactly once on every path (three error returns + one success in both collectSpendRequestAnswers and collectAnswers), so total sends = counter ≤ buffer capacity.
one success in both collectSpendRequestAnswers and collectAnswers), so total sends = counter ≤ buffer capacity.

## Todo 2 — drain after the receive loop exits:

Not needed, and here's the reasoning. The drain in the classic fix exists to unblock senders. But with the channel buffered to capacity, an in-flight worker completes its Send into the buffer and exits even when Collect has already returned early on timeout (collector.go:85) or cancellation (:83). Nothing holds a reference to the collector after Call returns, so the channel and any unreceived buffered answers are garbage-collected together. There's no blocked goroutine to rescue, so there's nothing to drain.