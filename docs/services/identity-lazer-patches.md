# Local patches to lazer

The [lazer identity module](identity-lazer.md) links the lattice library
[lazer](https://github.com/lazer-crypto/lazer) at the pinned upstream commit `b958e24`
(`LAZER_COMMIT` in `lazer.mk`). Upstream lazer at that commit has defects that matter for the
[anonymous credential scheme](identity-lazer-anoncreds.md): memory-safety and availability issues on
untrusted input, a discrete Gaussian sampler with process-global state, and deviations of the proof
system from the protocol it implements, some of which break soundness. Panurus fixes them with a
series of patch files in `x/token/services/identity/lazer/patches/`, which `make lazer-install`
applies in lexical order to a copy of the lazer sources before building. The fixes live here first and
are not waiting for upstream: an upstream reference is given where an issue or a pull request exists.

This page describes each patch: the upstream code it touches, what goes wrong without it, why it
matters for this scheme, the fix, and its compatibility. How the series is applied and checked
(strict patching, `make lazer-check`, `./.lazer/REVISION`, how to change a patch) is described in
[Local patches](identity-lazer.md#local-patches) of the module documentation.

| Group | Patches | Property |
|---|---|---|
| Portability | 0000 | correct arithmetic and builds on arm64 and macOS |
| Robustness and hardening | 0001, 0002, 0003, 0005 | memory safety on hostile input, availability, wiping of secrets |
| Sampler | 0004 | zero-knowledge of presentations, safe concurrent proofs |
| Proof-system soundness and zero-knowledge | 0006–0010 | unforgeability, binding of nyms to attributes, zero-knowledge |
| Performance and sharing | 0011 | public matrices shared read-only by concurrent provers and verifiers |

The three proof systems of the scheme are referred to as in the
[scheme description](identity-lazer-anoncreds.md#8-the-proof-systems-and-their-parameters): $P_1$ (issuance),
$P_2$ (presentation) and popen (the signature of knowledge of a nym opening, and the proof that a nym commits to
a public attribute, which share one parameter set).

---

## Portability

### 0000-lazer-pr33-arm64-int-mod

The two commits of [lazer-crypto/lazer#33](https://github.com/lazer-crypto/lazer/pull/33), kept as
`git format-patch` mails so that the series does not depend on a commit that only exists on a fork.

- **`int_mod` with zero-padded moduli** (`src/int.c`). `int_mod` passes the full limb count of the modulus to
  GMP's `mpn_sec_div_r`, which requires the most significant limb of the divisor to be non-zero. A modulus stored
  with the limb count of a larger one violates that precondition and the remainder is undefined. lazer's own
  `abdlop` and `lnp-quad` tests hit this case (a decomposition modulus $m\approx2^{58}$ stored with the limbs of a
  76-bit $q$): on x86-64 the result happens to be correct, on arm64 it lies far outside $(-m,m)$ and `abdlop_verify`
  rejects honest proofs. The proof moduli of this scheme (43, 51 and 38 bits) fit in one 64-bit limb, so its own
  parameters do not reach this case; the fix matters for lazer's test suite on arm64. The patch strips the zero top limbs first, as `int_div` does, and zeroes the upper limbs
  of the result.
- **arm64 and macOS builds.** `config.h` selects `TARGET_GENERIC` outside x86-64; `src/lazer-in2.h` includes x86
  intrinsics only on x86 and implements the carry/borrow helpers with `__int128` elsewhere; `src/aes256ctr.c` uses
  the ARMv8 AES instructions in the generic AES-256-CTR (the bit-serial software AES otherwise dominates the
  running time of every proof); the new `src/hexl.patch` makes Intel HEXL build with its scalar fallbacks and
  computes `MSB()` with `clz` instead of `log2l`, which is inexact where `long double` has 64 bits; the Makefile
  picks architecture-dependent flags (no `FALCON_AVX2`/`FMA`, `-mcpu=native`), finds Homebrew's GMP and MPFR and
  passes `-DCMAKE_POLICY_VERSION_MINIMUM=3.5` to cmake.

*Compatibility:* on x86-64 the compiler flags and code paths are unchanged (the HEXL `MSB()` change gives the same
results); this is established by code review of the patch. CI builds and tests on Linux amd64 and arm64; macOS is
built and tested locally, not in CI. Proofs and formats are unchanged. When the pull request is merged upstream, the patch is dropped and
`LAZER_COMMIT` moves to the merge commit; a source that already contains it does not accept the patch.

---

## Robustness and hardening

### 0001-coder-bounds-checks

*Upstream code:* the entropy coder `src/coder.c` and the proof encoders and decoders of `src/lin-proofs.c`.

*Problem.* The decoders scan unary codes (the hints, `coder_dec_ghint`, and the high parts of Gaussian elements,
`coder_dec_grandom`) and read fixed-width fields without knowing where the input ends; the encoders write without
knowing the capacity of the output. A crafted proof makes the verifier read past the end of its buffer.

*Why it matters.* Every signature carries a presentation, so any remote party that submits a signature controls
the input of the $P_2$ verifier; issuers decode $P_1$ proofs and masked attributes, users decode credentials.
Without bounds this is an out-of-bounds read on attacker-controlled input (memory safety, availability).

*Fix.* `coder_dec_begin_bnd` and `coder_enc_begin_bnd` start a coder that never reads or writes at or past the end
of its buffer. An access that would sets a sticky error (`coder_get_err`); every later operation is a no-op and
`coder_dec_end` fails. Every path is covered: bytes, fixed-width fields, hints and Gaussian unary codes, the end
marker, and the sparse encodings (whose header-controlled length is also checked). `lin_verifier_verify_ctx_bnd`
reads at most the given proof length, and `lin_prover_prove_ctx_bnd` fails instead of overflowing when a proof does
not fit. The scheme uses only the bounded entry points for untrusted data (`cbdc_dec_vec`, `cbdc_dec_cred` and all
proof verifications) and for its encoders.

*Compatibility.* No format change. The unbounded entry points remain, as wrappers passing `SIZE_MAX`. lazer's own
blind signature (`signer_sign`, `verifier_vrfy`, `user_sign` in `src/blindsig.c`), which this module does not use,
still decodes with the unbounded coder.

### 0002-blindsig-no-exit

*Upstream code:* `falcon_decode_pubkey` and `falcon_preimage_sample` in `src/blindsig.c`.

*Problem.* Both call `exit(EXIT_FAILURE)` on a malformed key, so a bad issuer key in a configuration terminates the
whole process (availability).

*Fix.* `falcon_decode_pubkey_chk` returns an error if the header byte is not `0x09` or a coefficient is
$\ge q$; `falcon_preimage_sample_chk` returns an error if the private key does not decode or sampling fails;
`falcon_check_keypair` checks that a private key is well formed and matches a public key. The secret temporaries
($f,g,F,G$, the scratch buffer and the PRNG state) are wiped before returning. The scheme uses the new functions in
`cbdc_pubkey_ok`, `cbdc_user_init`, `cbdc_verifier_init`, `cbdc_issuer_init` and `cbdc_issuer_issue`.
`TestBadPubKey` and `TestBadIssuerKey` check that malformed keys are refused with an error; the wiping of the
temporaries is established by code review.

*Compatibility.* The exiting functions keep their behaviour for existing callers and are implemented on top of the
new ones.

### 0003-zeroize-secrets

*Upstream code:* `src/bytes.c`, `src/poly.c`, `src/polyvec.c`, `src/polymat.c`, `src/lin-proofs.c`.

*Problem.* Secrets stay in freed memory: `bytes_clear` relies on `explicit_bzero`, which macOS defines as `bzero`, a
call the compiler may remove before a `free`; polynomials have no wipe operation; the LNP prover keeps its witness,
the copies made by `lin_prover_set_witness` and its expanded randomness.

*Fix.* `bytes_clear` writes through a volatile pointer. `poly_wipe`, `polyvec_wipe` and `polymat_wipe` zero the
coefficients and the CRT representation of a secret. The prover wipes its witness ($s_1$, $s_2$, $m$) in
`lin_prover_clear`, the temporary copies made by `lin_prover_set_witness`, and the expanded prover randomness and
internally generated coins in the prove functions. The scheme wipes its own secrets with the same functions
(`cbdc_vec_free_secret`).

*Compatibility.* No functional change. The wiping is established by code review; no test observes freed memory.

### 0005-lin-prover-wipe-witness

*Upstream code:* `src/lin-proofs.c`.

A prover state reused for several proofs keeps the last witness until `lin_prover_clear`.
`lin_prover_wipe_witness` wipes the witness ($s_1$, which also holds the appended $\upsilon$, and $m$) and the prover
randomness $s_2$, marks the witness as unset and keeps the public part, so the state can prove again after a new
`lin_prover_set_witness`. The module itself does not reuse prover states: every prover lives for one call and is
released with `lin_prover_clear`, which wipes it (0003).

*Compatibility.* New function only.

---

## Discrete Gaussian sampler

### 0004-grandom-sampler-state

*Upstream code:* `gaussian155`, `_grandom_sample` in `src/grandom.c` and `_intvec_grandom` in `src/intvec.c`, the
sampler from which the provers draw their masking vectors $\mathbf{y}$.

*Problems.*

1. `gaussian155` keeps its buffer of sign bits in function-level `static` variables. The buffer is shared by all
   threads, so two proofs running at the same time race on it (undefined behaviour); it carries bits of one random
   stream into the next call, so a sample depends on earlier, unrelated samples and a proof is not a function of
   its coins; and it is never wiped.
2. For a standard deviation $\sigma=1.55\cdot2^{\log_2 o}$ with $\log_2 o\ge24$, `_intvec_grandom` calls
   `_grandom_sample` for every element with the same seed and domain, and `_grandom_sample` reinitializes the random
   stream each time. All elements of a vector then get the same uniform part and the same coins, and differ only
   through the global sign bit: a vector of 64 elements with $\log_2 o=30$ takes about two distinct values.

*Why it matters.* Among the masks of the three proof systems, only the mask $\mathbf{y}_4$ of $P_2$ has
$\log_2 o\ge24$ (`log2stdev4 = 30` in `anoncreds_params.h`; all other masks have at most 23). $\mathbf{y}_4$ hides
the approximate-range projection $\mathbf{z}_4=\mathbf{y}_4+\beta_4R'\mathbf{s}_4$ (see
[Approximate range proofs](#approximate-range-proofs-z3-z4-and-the-matrices-r-and-r)), where $\mathbf{s}_4$ is the
quotient vector of the $P_2$ relation lifted from $\mathbb{Z}_p$ to $\mathbb{Z}$, a function of the witness. With
$\mathbf{y}_4$ almost constant, $\mathbf{z}_4$ reveals $R'\mathbf{s}_4$ nearly exactly. The eight credential rows of
$\mathbf{s}_4$ depend only on the credential, the masking randomness and the attributes, so they are the same in
every presentation of a wallet, and the projections accumulate over presentations. This is an analysis of the
code, not a demonstrated attack: it shows that without the patch the zero-knowledge argument for presentations does
not apply, and with it the guarantees that rest on it, namely their unlinkability and the secrecy of the credential,
of the hidden attributes (including `usk`) and of the nym openings. The patch is therefore required for the
zero-knowledge of presentations.

*Fix.* The sign-bit buffer belongs to the caller, is refilled from the caller's stream and is wiped when the caller
is done. A vector is sampled from a single random stream (`_grandom_sample_state`), as the $\log_2 o<24$ path does.
The temporaries of a large sample are wiped.

*Compatibility.* The distribution of a single sample is unchanged, verifiers are not affected, and proofs of either
build verify with either verifier. Proofs with fixed coins differ (the stream is used differently).
`TestGaussianSamplerLargeSigma` and `TestGaussianSamplerIsAFunctionOfItsSeed` check the two properties. There is no
upstream issue for it.

---

## Soundness and zero-knowledge of the proof system

Patches 0006–0010 fix deviations of lazer's implementation of the LNP proof system [LNP22] from the protocol. The
first subsection explains the parts of the protocol they touch.

!!! note "How these fixes are verified"
    The soundness fixes 0006–0009 are verified by code review against the upstream issue threads and the reporters'
    fixes, and by the honest-proof tests, which show that the patched prover and verifier agree. No test exercises
    the rejection of a malicious proof. The consequences described for each defect are analysis of the code and of
    the protocol, not demonstrated attacks on this scheme, unless stated otherwise.

### Background: the toolbox protocol in lazer

The *toolbox* is the protocol of [LNP22] that combines everything a typical lattice statement needs into one proof:
linear relations over $\mathcal{R}_q$, quadratic relations, and three kinds of norm constraints on blocks of the
witness (binary coefficients, exact Euclidean norms, and an approximate range bound). All of them are reduced to a
single quadratic relation over the committed values, which a quadratic proof (`lnp-quad`) then proves.

#### Where the code lives

The scheme proves its three relations with lazer's linear-relation front end (`lin_prover_prove_ctx_bnd`,
`lin_verifier_verify_ctx_bnd`). That front end does not call the toolbox of `src/lnp-tbox.c` (`lnp_tbox_prove`,
`lnp_tbox_verify`): `src/lin-proofs.c` contains a private, `static` copy of the toolbox protocol
(`_lnp_tbox_prove`, `_lnp_tbox_verify` and the `___schwartz_zippel_*` accumulators). Both copies share the lower
layers: the quadratic proof (`src/lnp-quad.c`, `src/lnp-quad-many.c`), the ABDLOP commitment (`src/abdlop.c`) and
rejection sampling (`src/rejection.c`). A fix applied to `src/lnp-tbox.c` alone, as the upstream issues propose,
therefore does not reach the proofs of this module. Patches 0006–0008 and the sign fix of 0010 change
`src/lin-proofs.c`; 0009 and the rejection fix of 0010 change the shared lower layers.

The protocol commits to the witness and to auxiliary vectors with an ABDLOP commitment, proves a set of linear and
quadratic equations about the committed values, and runs over $\mathcal{R}_q=\mathbb{Z}_q[X]/(X^{64}+1)$ with a
proof modulus $q$ of 43, 51 and 38 bits for $P_1$, $P_2$ and popen
([scheme description, §2.5](identity-lazer-anoncreds.md#25-zero-knowledge-proofs-from-lattices-lnp-and-lazer)).

#### Notation

- $\sigma_i=1.55\cdot2^{\texttt{log2stdev}_i}$ is the standard deviation of the Gaussian mask of response $i$
  (the `log2stdev` values of the generated headers).
- $M_i$ is the repetition constant of the rejection test on response $i$: a test accepts with probability about
  $1/M_i$, so the prover expects about $M_i$ attempts.
- $\mathbf{B}$ (bold) is the Falcon public key as a matrix over $\mathcal{R}_p$, as on the
  [scheme page](identity-lazer-anoncreds.md#41-nothing-up-my-sleeve-matrices); $B$ (not bold) is the BDLOP
  commitment matrix of the proof system. Block bounds are written $b_i$.
- $\sigma_{-1}$ is the automorphism $X\mapsto X^{-1}$ of $\mathcal{R}_q$. For $f,g\in\mathcal{R}_q$ the constant
  coefficient of $f\cdot\sigma_{-1}(g)$ is the inner product of their coefficient vectors, so a norm
  $\lVert\mathbf{x}\rVert^2$ is the constant coefficient of $\langle\mathbf{x},\sigma_{-1}(\mathbf{x})\rangle$, a
  quadratic expression in the committed $\mathbf{x}$. This is how norm constraints become quadratic relations.
- The automorphism also separates the coefficients $0$ and $d/2$ of one polynomial: since $X^{-d/2}=-X^{d/2}$ in
  $\mathcal{R}_q$, for $a=a_0+a_1X^{d/2}$ one has $(a+\sigma_{-1}(a))/2=a_0$ and
  $-X^{d/2}(a-\sigma_{-1}(a))/2=a_1$. lazer uses this to pack two scalars in one ring element: the signs $\beta_3$
  and $\beta_4$ are the coefficients $0$ and $d/2$ of one committed polynomial, and each accumulator below carries two
  independent combinations at the same two positions.

#### Fiat–Shamir: absorb every message before the challenges that bind it

The protocol is an interactive public-coin proof made non-interactive with the Fiat–Shamir transform [FS86]: every
verifier challenge is replaced by a hash of the statement and of all prover messages sent so far. lazer keeps a
running 32-byte state and updates it as `hash ← SHAKE128(hash ‖ encoding of the next message)`; challenges are
expanded from the current state and a domain separator.

Soundness of the interactive proof rests on the order of messages: the prover commits to a message $a$, then
receives a challenge $c$ it cannot predict. Fiat–Shamir preserves this only if $c$ is derived from a state that has
absorbed $a$. If a message is not absorbed before a challenge that is meant to bind it, a cheating prover sees the
challenge first and chooses the message as a function of it, which is exactly what the soundness proof excludes.
The rule is therefore: every prover message must be absorbed before every challenge that depends on it.

#### Compressing many equations: random linear combinations

The proof reduces many scalar equations $e_1(\mathbf{s})=\dots=e_M(\mathbf{s})=0$ over $\mathbb{Z}_q$ to a few,
by a random linear combination $\sum_j v_j e_j(\mathbf{s})=0$ with uniform challenges $v_j\in\mathbb{Z}_q$. If some
$e_j\neq0$, the combination vanishes with probability $1/q$ (a Schwartz–Zippel argument for a non-zero linear form).
Two consequences matter below:

- **Each combination needs its own challenges.** Two groups of equations combined with the *same* challenges are
  only checked through the sum of their combinations. Equations whose terms can have either sign can then trade
  errors against each other.
- **Repetition.** One combination has soundness error $1/q$. With $\lambda$ independent combinations the error is
  about $q^{-\lambda}$. lazer packs the combinations two by two into $\lambda/2$ ring elements: accumulator pair
  $i$ places two independent combinations into the coefficients $0$ and $d/2$ of a polynomial, masked by a
  committed random $g_i$ (its BDLOP commitment is $\mathbf{t}_g$), and the prover sends $h_i=g_i+\text{(accumulated equations)}$; the verifier checks that
  coefficients $0$ and $d/2$ of every $h_i$ vanish, and the quadratic proof then checks the relation between $h_i$
  and the committed values. All three parameter sets of the scheme use $\lambda=4$ (the `lnp_quad_eval_params_t` in
  `anoncreds_params.h`), so with independent challenges this step has error about $q^{-4}$: $2^{-172}$, $2^{-204}$
  and $2^{-152}$ for $P_1$, $P_2$ and popen.

#### Exact Euclidean norms

For each witness block $\mathbf{x}_i$ with an $\ell_2$ bound $b_i$, the prover commits to a binary vector
$\upsilon_i$ encoding the slack $b_i^2-\lVert\mathbf{x}_i\rVert^2\ge0$ in binary, and proves
$\lVert\mathbf{x}_i\rVert^2+\langle\mathbf{2}^{\star},\upsilon_i\rangle-b_i^2=0$ over $\mathbb{Z}_q$, where
$\mathbf{2}^{\star}=(1,2,4,\dots)$ and $\upsilon_i$ is proven binary. Since the slack term is non-negative, the
equation gives $\lVert\mathbf{x}_i\rVert\le b_i$, provided nothing wraps around mod $q$, which the approximate range
proof below guarantees. There is one such equation per block ($Z=2$, $10$ and $2$ blocks for $P_1$, $P_2$ and popen),
and each must be enforced separately: the terms $\lVert\mathbf{x}_i\rVert^2-b_i^2$ have either sign, so a sum of the
equations only bounds the total $\sum_i\lVert\mathbf{x}_i\rVert^2\le\sum_ib_i^2$.

#### Approximate range proofs: z3, z4 and the matrices R and R'

Exact norms are computed mod $q$, so the prover must also show that the vectors involved are far smaller than $q$.
This is an approximate range proof based on the Johnson–Lindenstrauss lemma [GHL22, LNP22]: for a random matrix
$R$ with small entries and 256 rows, $\lVert R\mathbf{s}\rVert$ is close to a fixed multiple of
$\lVert\mathbf{s}\rVert$ with overwhelming probability, so a short projection implies a short $\mathbf{s}$ up to a
slack factor (ψ in the generated headers). The prover sends

$$\mathbf{z}_3=\mathbf{y}_3+\beta_3R\,\mathbf{s}_3,\qquad \mathbf{z}_4=\mathbf{y}_4+\beta_4R'\mathbf{s}_4,$$

where $\mathbf{s}_3$ collects the binary block (τ for $P_2$, 8 ring elements; none for $P_1$ and popen), the
exact-norm blocks and the $\upsilon_i$, $\mathbf{s}_4$ is the quotient vector of the lifted relation $\mathbf{A}\mathbf{w}+\mathbf{u}=p\,\mathbf{s}_4$ (8, 14 and 2 rows for $P_1$, $P_2$, popen),
$\mathbf{y}_3,\mathbf{y}_4$ are committed Gaussian masks and $\beta_3,\beta_4\in\{\pm1\}$ are committed random
signs. $R$ and $R'$ are derived from the Fiat–Shamir state *before* $\mathbf{z}_3,\mathbf{z}_4$, because the prover
computes $\mathbf{z}_3,\mathbf{z}_4$ from them. The verifier checks the norms of $\mathbf{z}_3,\mathbf{z}_4$, and the
256 linear equations per projection that tie them to the committed values are compressed with challenges $V_3$,
$V_4$; those must come *after* $\mathbf{z}_3,\mathbf{z}_4$ are absorbed, otherwise a prover who knows $V_3$ can
publish a short $\mathbf{z}_3'\neq\mathbf{z}_3$ with $V_3\mathbf{z}_3'\equiv V_3\mathbf{z}_3$, an easy inhomogeneous
SIS instance with $\lambda$ rows.

#### The ABDLOP opening: z1 and z2 = (z21, z22)

The ABDLOP commitment [LNP22] has an Ajtai part $\mathbf{t}_A=A_1\mathbf{s}_1+A_2\mathbf{s}_2$, with fresh ternary
randomness $\mathbf{s}_2$ ($m_2=57$, $64$, $53$ ring elements for $P_1$, $P_2$, popen) and the committed secret
$\mathbf{s}_1$. Here $\mathbf{s}_1$ is the whole witness $\mathbf{w}$ of the relation
([scheme description, §2.5](identity-lazer-anoncreds.md#25-zero-knowledge-proofs-from-lattices-lnp-and-lazer)) followed
by the $Z$ slack vectors $\upsilon_i$, so $m_1=21+2=23$, $75+10=85$ and $11+2=13$; it is not the Falcon preimage
$\mathbf{s}_1$ of the credential, which is one block of $\mathbf{w}$ in $P_2$. The commitment also has a BDLOP part $\mathbf{t}_B=B\mathbf{s}_2+\mathbf{m}$ that commits to the auxiliary messages ($\mathbf{y}_3$,
$\mathbf{y}_4$, the signs, the $g_i$). The opening proof sends masked responses
$\mathbf{z}_1=\mathbf{y}_1+c\,\mathbf{s}_1$ and $\mathbf{z}_2=\mathbf{y}_2+c\,\mathbf{s}_2$ for a short challenge
$c$. The commitment and the opening are compressed as in Dilithium: $\mathbf{t}_A$ is published without its $D$
low-order bits, and $A_2=[A_2'\mid I]$, so $\mathbf{z}_2=(\mathbf{z}_{21},\mathbf{z}_{22})$ and only $\mathbf{z}_{21}$ is
sent. The verifier computes $\mathbf{w}=A_1\mathbf{z}_1+A_2'\mathbf{z}_{21}-c\,2^D\mathbf{t}_A$; its high-order part
$\mathbf{w}_1$ with respect to the decomposition parameter $\gamma$ (both $D$ and $\gamma$ are printed under
"Compression" in the generated headers) is what the prover's first message commits to, and a short *hint* sent in
the proof lets the verifier recover $\mathbf{w}_1$ despite the dropped bits. The second part of the response is then
$\mathbf{z}_{22}=\mathbf{w}-\gamma\mathbf{w}_1$. Knowledge soundness extracts an M-SIS solution from the differences of two
accepting responses, and the generated parameters size that M-SIS instance (for example MSIS(20, 139, $2^{40.6}$) for
$P_2$) for a bound on the whole $\mathbf{z}_2$, as in [LNP22, Fig. 18]. The verifier must therefore bound
$\lVert(\mathbf{z}_{21},\mathbf{z}_{22})\rVert$, not only $\mathbf{z}_{22}$.

#### Rejection sampling

A response $\mathbf{z}=\mathbf{y}+\mathbf{v}$ with a secret-dependent shift $\mathbf{v}$ leaks the shift unless it is
accepted only with a probability that cancels it (Fiat–Shamir with aborts [Lyu09, Lyu12]). The generated
parameters choose one test per response, and its correctness is what zero-knowledge rests on:

- $\mathbf{z}_1$: standard rejection, $\Pr[\text{accept}]=D_\sigma(\mathbf{z})/(M\,D_{\sigma,\mathbf{v}}(\mathbf{z}))$,
  which makes $\mathbf{z}_1$ distributed as $D_{\sigma_1}$, independent of $\mathbf{v}=c\,\mathbf{s}_1$.
- $\mathbf{z}_3,\mathbf{z}_4$: bimodal rejection [DDLL13], which applies to $\mathbf{z}=\mathbf{y}+\beta\mathbf{v}$
  with a *uniform* secret sign $\beta$ and accepts with probability
  $1/\big(M\exp(-\lVert\mathbf{v}\rVert^2/2\sigma^2)\cosh(\langle\mathbf{z},\mathbf{v}\rangle/\sigma^2)\big)$.
  The output is $D_\sigma$ only if $\beta$ is uniform and the $\sigma$ in the test is that of the mask.
- $\mathbf{z}_2$ (`rej2 = 2`): the one-time rejection Rej1 of Lyubashevsky, Nguyen and Seiler [LNS21], meant for a
  secret used in a single proof, as the ABDLOP randomness $\mathbf{s}_2$ is. With $\mathbf{v}=c\,\mathbf{s}_2$ it
  rejects if $\langle\mathbf{z},\mathbf{v}\rangle<0$ and otherwise accepts with probability
  $\exp\big((\lVert\mathbf{v}\rVert^2-2\langle\mathbf{z},\mathbf{v}\rangle)/2\sigma_2^2\big)/M_2$, for
  $M_2=\exp(1/(2\gamma^2))$ as computed by the code generator. It accepts about half as often as a standard test with
  the same $M_2$.

### 0006-tbox-fiat-shamir-h-z3-z4

*Upstream reference:* [lazer-crypto/lazer#28](https://github.com/lazer-crypto/lazer/issues/28). *Upstream code:*
`_lnp_tbox_prove` and `_lnp_tbox_verify` in `src/lin-proofs.c` (and the same functions of `src/lnp-tbox.c`).

*Problem.* Three prover messages are not bound by the challenges meant to bind them:

1. **$h$ is never hashed.** The challenge $\mu$ that combines all quadratic equations in `lnp_quad_many_*`, and the
   final challenge, are derived from the state after $\mathbf{t}_g$, before $h$ exists. $h_i$ enters the combined
   relation only through a constant term, so a cheating prover who knows $\mu$ solves one linear equation over
   $\mathcal{R}_q$ for $h$ (with coefficients $0$ and $d/2$ fixed to zero) and makes every evaluation equation and
   every quadratic input equation hold in combination, whatever its witness. By this analysis (from the issue
   thread; no cheating prover for it is reported) the binding of the whole statement is lost.
2. **$\mathbf{z}_3,\mathbf{z}_4$ are never hashed.** $V_3,V_4$ come from the state before $\mathbf{z}_3,\mathbf{z}_4$,
   which removes the approximate range guarantee on which the exact-norm proofs rely (no wrap-around mod $q$).
3. **$V_3,V_4$ come from the state before $\mathbf{t}_g$**, while they feed the same accumulators $h_i=g_i+\dots$ as
   the other equations. A prover who knows $V_3,V_4$ before committing to $g$ chooses the coefficients $0$ and $d/2$
   of $g$ to cancel an error in the $\mathbf{z}_3,\mathbf{z}_4$ equations.

*Why it matters.* Knowledge soundness of $P_1$, $P_2$ and popen. Without it, the proof does not establish the
credential equation, the norm bounds or the commitment rows of $P_2$, on which credential unforgeability, the
binding of nyms to certified attributes (auditability) and signature unforgeability rest.

*Fix.* $\mathbf{z}_3$ and $\mathbf{z}_4$ are absorbed right after they are computed (prover) or norm-checked
(verifier); $V_3,V_4$ are drawn from the state after $\mathbf{t}_g$; $h$ is absorbed before
`lnp_quad_many_prove`/`_verify`, so $\mu$ and the final challenge depend on it. $R$ and $R'$ stay derived from the
state before $\mathbf{z}_3,\mathbf{z}_4$ (kept as `hashr`). Each round absorbs the proof's own encoding of the message
(Gaussian coding for $\mathbf{z}_3,\mathbf{z}_4$, uniform coding mod $q$ for $h$), which is injective and needs no
length fields; the new `coder_grandom3_nbits` (`src/coder.c`) sizes the buffer for $\mathbf{z}_3,\mathbf{z}_4$
exactly. The patch ports the fix proposed in the issue thread to the copy in `src/lin-proofs.c`.

*Compatibility.* Prover and verifier; the transcript changes, so proofs with and without the patch do not verify
against each other. Proof sizes and parameters are unchanged.

### 0007-tbox-l2-equation-domains

*Upstream reference:* [lazer-crypto/lazer#25](https://github.com/lazer-crypto/lazer/issues/25). *Upstream code:*
`___schwartz_zippel_accumulate_l2` in `src/lin-proofs.c`.

*Problem.* The $Z$ exact-$\ell_2$ equations are combined with challenges from one and the same domain, so the
verifier checks only their sum, $\sum_i\big(\lVert\mathbf{x}_i\rVert^2+\langle\mathbf{2}^{\star},\upsilon_i\rangle-b_i^2\big)=0$.
That bounds only the total norm: a prover moves norm from one block to another through the $\upsilon$ slack, with
probability 1 and at no extra cost (the rejection sampling only looks at the total).

*Why it matters for $P_2$.* The blocks of $P_2$ are, from `anoncreds/params/anoncreds_p2_params.py`,
$[109,\ \text{binary }\tau,\ \sqrt{34034726},\ 5\times64,\ 3\times39]$, so the pooled bound is
$\sqrt{109^2+34034726+5\cdot64^2+3\cdot39^2}=\sqrt{34071650}\approx5837$. An honest Falcon preimage has norm about
$\sigma\sqrt{1024}\approx5302$, which leaves a slack of about $\sqrt{5837^2-5302^2}\approx2440$ that a cheating prover
can place in a single block (the honest norms of the other blocks change this by less than 1%). Placed in an opening $\boldsymbol\rho_j$, it turns the nym rows
$\mathbf{a}_0m_{k_j}+\mathbf{A}'_{\mathrm{com}}\boldsymbol\rho_j=\mathsf{nym}_j$ into an inhomogeneous SIS problem for
the $2\times10$ block $\mathbf{A}'_{\mathrm{com}}$ with norm bound about 2440 instead of about 75: given any target nym
(for example someone else's `nymEID`) and the prover's own certified attribute, find such a $\boldsymbol\rho_j$. With
the root-Hermite estimate used in the
[scheme description, §6.2](identity-lazer-anoncreds.md#62-signature-of-knowledge-of-an-opening), norm 75 needs
$\delta\approx1.0039$ (about 120 bits), while norm 2440 needs only $\delta\approx1.013$, which lattice reduction with
a small block size reaches. These figures are estimates, but they mean that without the patch the binding of nyms to
certified attributes, and with it audit soundness and the rule that only the owner of a pseudonym can sign for it,
would not hold at any meaningful security level. The same pooling applies to $P_1$ (bounds 109 and 91, pooled
$\approx142$) and to popen (64 and 39, pooled $\approx75$).

*Fix.* Equation $i$ uses domain `dom + i`. The domains reserved for this step end below the $\mathbf{z}_4$ domain
$\lambda(2(d-1)+2+Z)$, so no other challenge set moves.

*Compatibility.* Prover and verifier; transcript change; sizes unchanged.

### 0008-tbox-accumulator-challenge-pairs

*Upstream reference:* [lazer-crypto/lazer#24](https://github.com/lazer-crypto/lazer/issues/24). *Upstream code:*
`___schwartz_zippel_accumulate2` in `src/lin-proofs.c`.

*Problem.* The function draws $2M$ challenges from `(seed, dom)` inside its loop over the $\lambda/2$ accumulator
pairs, so every pair gets the same two rows of challenges and the step performs only two independent combinations,
whatever $\lambda$. In the copy in `src/lin-proofs.c` it is called by the accumulators of the $\upsilon$-binarity
equations, of the binary block ($\tau$) and of the exact-$\ell_2$ equations; the $\beta_3,\beta_4$ accumulators draw
their own $(d-1)\lambda$ challenges and the $\mathbf{z}_3,\mathbf{z}_4$ accumulators a $\lambda$-row matrix, so they are
not affected. For these three groups the soundness error is about $q^{-2}$ instead of $q^{-\lambda}$: computed from
the parameters ($\lambda=4$ and the size of $q$), about $2^{-86}$, $2^{-102}$ and $2^{-76}$ for $P_1$, $P_2$ and
popen, above the $2^{-127}$ knowledge error the code generator assumes for the whole protocol.

*Fix.* A $\lambda\times M$ challenge matrix is drawn once, and pair $i$ uses rows $2i$ and $2i+1$, as
`_schwartz_zippel_int` in `src/lnp-quad-eval.c` and the $\beta_3,\beta_4$ accumulators do.

*Compatibility.* Prover and verifier; transcript change; sizes unchanged.

### 0009-quad-verify-bound-z21

*Upstream reference:* [lazer-crypto/lazer#26](https://github.com/lazer-crypto/lazer/issues/26). *Upstream code:*
`lnp_quad_verify` (`src/lnp-quad.c`), which both toolbox copies reach through `lnp_quad_many_verify`
(`src/lnp-quad-many.c`), and the standalone ABDLOP verifier `abdlop_verify` (`src/abdlop.c`), which has the same final
check and is not used by this module.

*Problem.* The verifiers bound $\lVert\mathbf{z}_1\rVert$, $\lVert\mathbf{w}-\gamma\mathbf{w}_1\rVert$ (that is,
$\mathbf{z}_{22}$) and the hint, but never $\mathbf{z}_{21}$, which is only limited by the proof length. The prover
rejects on, and the parameters assume, a bound on all of $\mathbf{z}_2$.

*Why it matters.* The extracted M-SIS solution can then be far longer than the bound the parameters were sized for,
so knowledge soundness of every proof of the scheme holds only for a much weaker M-SIS instance than the one in the
generated headers. No concrete attack is known; the issue reports a re-estimate for other parameter sets, and this
module's sets have not been re-estimated without the check.

*Fix.* Both verifiers check $\lVert\mathbf{z}_{21}\rVert^2+\lVert\mathbf{w}-\gamma\mathbf{w}_1\rVert^2\le B_{z_2}^2$
(`Bsqr`), the quantity the prover rejects on.

*Compatibility.* Verifier only. Honest proofs satisfy the check, and the transcript does not change.

### 0010-prover-zk-z2-rejection-beta-signs

*Upstream reference:* [lazer-crypto/lazer#31](https://github.com/lazer-crypto/lazer/issues/31). *Upstream code:*
`lnp_quad_prove` (`src/lnp-quad.c`), `src/rejection.c`, and the sign sampling in `__lnp_tbox_compute_z34`
(`src/lin-proofs.c`).

*Problems.*

1. **$\mathbf{z}_2$ rejection.** For `rej2 == 2`, `lnp_quad_prove` calls the bimodal test with $\sigma_1^2$ instead of
   $\sigma_2^2$ and without a sign. With $\sigma_1\gg\sigma_2$ (for $P_2$, $\sigma_1/\sigma_2=2^{11}$) the acceptance
   probability is about $1/M_2$ whatever $\mathbf{z}_2$, so the step costs repetitions but does not shape
   $\mathbf{z}_2$, which is published distributed roughly as $D_{\sigma_2}$ shifted by $c\,\mathbf{s}_2$. Even with
   $\sigma_2$, the bimodal test without a random sign does not output $D_{\sigma_2}$.
2. **$\beta_4$ sign.** In the toolbox prover the sign of $\beta_4$ is read from bit $8-n+1$ of a byte of random bits
   with $n$ bits left: on every fourth attempt that is bit 8 of a `uint8_t`, always 0, so $\beta_4=+1$; $\beta_3$ of
   the next attempt reuses $\beta_4$'s bit, and half of the bits are never used.

*Why it matters.* Zero-knowledge, and therefore the unlinkability of presentations and the secrecy of the witness:
$\mathbf{s}_2$ is the randomness of the commitment that hides the witness and the auxiliary messages, and the
simulator assumes $\mathbf{z}_2$ independent of it and $\beta_3,\beta_4$ uniform. Both deviations are found by
analysis of the code; the upstream estimate of the leakage is small per proof, but the zero-knowledge argument does
not apply as implemented.

*Fix.* The new `rej_onetime` (`src/rejection.c`) implements Rej1 of [LNS21] as described in
[Rejection sampling](#rejection-sampling) and is called with $\sigma_2^2$; it always draws the uniform value, so the
use of the random stream does not depend on the branch. The bimodal test stays for $\mathbf{z}_3,\mathbf{z}_4$, which
do have a sign. $\beta_4$ reads the next unused bit, refilling the byte when needed, and the sign bits are wiped after
use.

*Compatibility.* Prover only: proofs from either prover verify with either verifier. The expected number of
$\mathbf{z}_2$ attempts roughly doubles (by analysis: Rej1 accepts with probability about $1/(2M_2)$; the issue
thread reports a matching sampling measurement). The "repetition rate" printed in the generated headers is
$M_1M_2M_3M_4$, which counts an acceptance probability of $1/M_2$ for $\mathbf{z}_2$, so it underestimates the
number of runs by about a factor of two.

### Scope and testing of 0006–0010

- The patches fix the entry points this module uses. lazer's own toolbox in `src/lnp-tbox.c`
  (`lnp_tbox_prove`/`lnp_tbox_verify`) still has the defects of #24, #25, #28 and the $\beta_4$ sign, and
  `src/lnp-quad-eval.c` does not absorb $h$ before `lnp_quad_many_*` either. Nothing in this module calls them.
- The domain overlap between the last $\beta_4$ equation and the $\upsilon$ equation, noted in #25 and #28 and
  believed harmless there, is not changed.
- The honest-proof tests show that the patched prover and verifier agree. The fixes for #28, #25 and #26 were checked
  by code review against the issue threads and the reporters' fixes; there are no negative tests with a cheating
  prover, and #24 has no dedicated test.

---

## Shared public parameters

### 0011-lin-shared-public-params

*Upstream code:* `lin_prover_init`, `lin_verifier_init` and the internal state of `src/lin-proofs.c`.

*Background: the CRT representation.* A polynomial of $\mathcal{R}_q$ in lazer has a coefficient representation
and, optionally, a CRT (NTT) representation, the evaluation form in which products are computed coefficient-wise.
A `poly_t` carries both and a flag saying which is current. `poly_mul` and the matrix-vector products call
`_tocrt` on both operands, which computes the CRT form *in place* (allocating it if needed and setting the flag)
when it is not current; when it is, the call does nothing. A product therefore writes to its operands, even to
operands it only reads logically.

*Problem.* `lin_prover_init` and `lin_verifier_init` expand the public ABDLOP matrices $A_1$, $A_2'$ and $B'$ from
the parameter seed for every prover and verifier, and the first proof converts them in place. A state cannot be
shared by concurrent proofs, and neither can its matrices: two threads converting the same unconverted matrix
write the same memory (a data race). Each prover and verifier also pays for its own copy of the largest data of
the proof system.

*Fix.* `lin_pp_init` expands the matrices of a proof system once and converts them to the CRT domain
(`polymat_tocrt`). `lin_prover_init_pp` and `lin_verifier_init_pp` set up a prover or a verifier that borrows them
(shallow copies of the matrix headers, flagged `pp_borrowed` so that `lin_*_clear` does not free them) and takes the
parameters from the `lin_pp_t`, so the two cannot be mismatched. After the conversion the protocol uses these
matrices only as the left operand of products, whose conversion is then a no-op, so nothing writes them and any
number of provers and verifiers borrowing one `lin_pp_t` may run concurrently. `lin_pp_clear` releases it.

*How the scheme uses it.* `cbdc_pp_do_init` (run once, under `pthread_once`) creates one `lin_pp_t` each for $P_1$,
$P_2$ and popen. Every prover and verifier of `anoncreds.c` borrows them, except a test-only path that sets up provers with private
matrices, and every prover lives for one call:
`cbdc_user_maskattrs` sets up and clears the $P_1$ prover, `cbdc_user_presgen` the $P_2$ prover, the SoK and
equality-of-opening provers likewise. As approximate figures from a one-off count of the allocations of the lazer library (not reproduced by a test), the
shared matrices take about 20 MB per process ($P_2$ 11.39 MB, $P_1$ 4.89 MB, popen 3.99 MB), and a $P_2$ prover set up from them about 3.06 MB per call, a
$P_2$ verifier about 2.72 MB.

*Invariant.* Nothing may write a borrowed matrix: it must stay in CRT form and only ever be the left operand of a
product, never an argument of `polymat_fromcrt`, `_mod`, `_redc`, `_set` or of a statement setter (the statement
setters of `lin-proofs.c` convert their arguments in place, which is why the scheme passes them private copies,
`cbdc_mat_copy`). A `lin_pp_t` must outlive every state that borrows it. A change to lazer that breaks either rule
reintroduces a data race that Go's race detector cannot see, because it is in C. `TestUserConcurrentUse` checks a
digest of the shared matrices after concurrent proving and verification, and `TestSharedPublicParamsSameProofs`
checks that presentations and signatures of knowledge with fixed coins are identical with shared and with private
matrices.

*Compatibility.* `lin_prover_init` and `lin_verifier_init` are unchanged; proofs do not change.

---

## Compatibility summary

| Patch | Side | Transcript or format | Upstream |
|---|---|---|---|
| 0000 | build, arithmetic | unchanged | [#33](https://github.com/lazer-crypto/lazer/pull/33) |
| 0001 | decoders, encoders | unchanged | — |
| 0002 | key handling | unchanged | — |
| 0003, 0005 | prover memory | unchanged | — |
| 0004 | prover sampling | unchanged (fixed-coin proofs differ) | — |
| 0006 | prover and verifier | **changed** | [#28](https://github.com/lazer-crypto/lazer/issues/28) |
| 0007 | prover and verifier | **changed** | [#25](https://github.com/lazer-crypto/lazer/issues/25) |
| 0008 | prover and verifier | **changed** | [#24](https://github.com/lazer-crypto/lazer/issues/24) |
| 0009 | verifier | unchanged | [#26](https://github.com/lazer-crypto/lazer/issues/26) |
| 0010 | prover | unchanged (fixed-coin proofs differ) | [#31](https://github.com/lazer-crypto/lazer/issues/31) |
| 0011 | setup | unchanged | — |

Because of 0006–0008, proofs made by a lazer without the series and by a lazer with it do not verify against each
other. Credentials, nyms, parameters and proof sizes are the same.

## References

- [DDLL13] L. Ducas, A. Durmus, T. Lepoint, V. Lyubashevsky. *Lattice Signatures and Bimodal Gaussians.* CRYPTO 2013. [ePrint 2013/383](https://eprint.iacr.org/2013/383).
- [FS86] A. Fiat, A. Shamir. *How to Prove Yourself: Practical Solutions to Identification and Signature Problems.* CRYPTO 1986.
- [GHL22] C. Gentry, S. Halevi, V. Lyubashevsky. *Practical Non-interactive Publicly Verifiable Secret Sharing with Thousands of Parties.* EUROCRYPT 2022. [ePrint 2021/1397](https://eprint.iacr.org/2021/1397).
- [LNP22] V. Lyubashevsky, N. K. Nguyen, M. Plançon. *Lattice-Based Zero-Knowledge Proofs and Applications: Shorter, Simpler, and More General.* CRYPTO 2022. [ePrint 2022/284](https://eprint.iacr.org/2022/284).
- [LNS21] V. Lyubashevsky, N. K. Nguyen, G. Seiler. *Shorter Lattice-Based Zero-Knowledge Proofs via One-Time Commitments.* PKC 2021. [ePrint 2020/1448](https://eprint.iacr.org/2020/1448).
- [Lyu09] V. Lyubashevsky. *Fiat-Shamir with Aborts: Applications to Lattice and Factoring-Based Signatures.* ASIACRYPT 2009.
- [Lyu12] V. Lyubashevsky. *Lattice Signatures without Trapdoors.* EUROCRYPT 2012.
