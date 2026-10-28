/*
 * Derived from the lazer library (https://github.com/lazer-crypto/lazer),
 * Copyright (c) 2022-2026 IBM, MIT license, see LICENSE.lazer.
 */

/*
 * CBDC anonymous credentials, C port of python/cbdc/cbdc.py in the lazer
 * repository (which is the reference of the scheme).
 *
 * Attributes (5 x 32 bytes) are encoded as 5 polynomials over
 * Rp = Z_12289[X]/(X^64+1). The issuer blindly signs (Falcon-512 preimage
 * sampling) t + Atau*tau + c0, with the commitment t = Ar*r + Am*m to the
 * attributes, a binary tau chosen by the issuer and a public non-zero c0, after checking a
 * proof P1 that t is well-formed and consistent with the attributes revealed
 * to it. A presentation P2 proves knowledge of a credential and selectively
 * discloses attributes to the verifier. Attributes can additionally be
 * committed to (nym = A*(attr,r)) with a standalone signature of knowledge.
 */
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "anoncreds.h"

/* proof system parameters p1_param, p2_param, popen_param, generated from params/ */
#include "anoncreds_params.h"

#define CBDC_L2SQR_BND_R (109 * 109) /* l2(r)^2, wl2[0] in anoncreds_p1_params.py */
#define CBDC_LOG2O_BLINDSIG 7        /* ceil(log(165/1.55,2)) */

/* ring Rp. The proof systems require polys over this ring. */
static const limb_t cbdc_q_limbs[] = { CBDC_MOD };
static const int_t cbdc_q = { { (limb_t *)cbdc_q_limbs, 1, 0 } };
static const limb_t cbdc_inv2_limbs[] = { (CBDC_MOD - 1) / 2 }; /* -6144 */
static const int_t cbdc_inv2 = { { (limb_t *)cbdc_inv2_limbs, 1, 1 } };
static const limb_t cbdc_Pmodq_limbs[] = { 0 };
static const int_t cbdc_Pmodq = { { (limb_t *)cbdc_Pmodq_limbs, 1, 0 } };

static const polyring_t cbdc_ring
    = { { cbdc_q, CBDC_DEG, CBDC_LOGMOD, 6, moduli_d64, 1, cbdc_Pmodq, NULL,
          cbdc_inv2 } };
/* Falcon-512 ring, only used to convert the Falcon pubkey to Rp */
static const polyring_t cbdc_falcon_ring
    = { { cbdc_q, 512, CBDC_LOGMOD, 0, NULL, 0, cbdc_Pmodq, NULL,
          cbdc_inv2 } };

/* public parameters, expanded once from seeds */
static int cbdc_pp_ready = 0;
static uint8_t cbdc_blindsigpp[32];
static uint8_t cbdc_p1pp[32];
static uint8_t cbdc_p2pp[32];
static uint8_t cbdc_popenpp[32];
static polymat_t cbdc_AR;   /* 8x16 */
static polymat_t cbdc_AM;   /* 8x5 */
static polymat_t cbdc_ATAU; /* 8x8 */
static polymat_t cbdc_Acom; /* CBDC_COM_ROWSx11 commitment key */

static void
cbdc_seed (uint8_t out[32], uint8_t b)
{
  shake128_state_t hstate;

  shake128_init (hstate);
  shake128_absorb (hstate, &b, 1);
  shake128_squeeze (hstate, out, 32);
  shake128_clear (hstate);
}

static void
cbdc_urandom_mat (polymat_t M, unsigned int rows, unsigned int cols,
                  const uint8_t seed[32], uint32_t dom)
{
  INT_T (mod, 1);

  int_set_i64 (mod, CBDC_MOD);
  polymat_alloc (M, cbdc_ring, rows, cols);
  polymat_urandom (M, mod, CBDC_LOGMOD, seed, dom);
  polymat_redc (M, M);
}

static pthread_once_t cbdc_pp_once = PTHREAD_ONCE_INIT;

/*
 * The public matrices below are read-only after initialization. Operations
 * that convert their operands in place (polyvec_mul, lin_*_set_statement)
 * must work on a private copy, see cbdc_mat_copy.
 */
/*
 * The generated parameters (anoncreds_params.h) and the constants of anoncreds.h must
 * agree: p2 has one block of CBDC_COM_ROWS rows for each of CBDC_MAXCOM
 * commitments on top of the 8 rows of the credential, popen has CBDC_COM_ROWS
 * rows. Returns 1 if they do.
 */
int
cbdc_params_ok (void)
{
  return p1_param->tbox_params->nprime == 8
         && p2_param->tbox_params->nprime == 8 + CBDC_MAXCOM * CBDC_COM_ROWS
         && popen_param->tbox_params->nprime == CBDC_COM_ROWS;
}

static void
cbdc_pp_do_init (void)
{
  lazer_init ();

  if (!cbdc_params_ok ())
    {
      /* a build with inconsistent parameters would produce unsound statements */
      fprintf (stderr, "cbdc: parameters do not match CBDC_COM_ROWS/CBDC_MAXCOM\n");
      abort ();
    }

  cbdc_seed (cbdc_blindsigpp, 0);
  cbdc_seed (cbdc_p1pp, 1);
  cbdc_seed (cbdc_p2pp, 2);
  cbdc_seed (cbdc_popenpp, 3);

  cbdc_urandom_mat (cbdc_AR, 8, 16, cbdc_blindsigpp, 1);
  cbdc_urandom_mat (cbdc_AM, 8, CBDC_NATTRS, cbdc_blindsigpp, 2);
  cbdc_urandom_mat (cbdc_ATAU, 8, 8, cbdc_blindsigpp, 3);
  cbdc_urandom_mat (cbdc_Acom, CBDC_COM_ROWS, 11, cbdc_popenpp, 1);

  cbdc_pp_ready = 1;
}

static void
cbdc_pp_init (void)
{
  pthread_once (&cbdc_pp_once, cbdc_pp_do_init);
}

/* private copy of a public matrix, to be freed by the caller */
static void
cbdc_mat_copy (polymat_t out, polymat_t src)
{
  polymat_alloc (out, cbdc_ring, polymat_get_nrows (src),
                 polymat_get_ncols (src));
  polymat_set (out, src);
}

/* buffer sizes */

#define CBDC_PROOFLEN(params) ((size_t)lin_params_get_prooflen (params) * 6 / 5 + 1)

size_t
cbdc_maxlen_masked_attrs (void)
{
  /* encoding of t (8 polys, 14 bits per coeff) + P1 */
  return 1024 + CBDC_PROOFLEN (p1_param);
}

size_t
cbdc_maxlen_cred (void)
{
  return 3000;
}

size_t
cbdc_maxlen_pres (void)
{
  return CBDC_PROOFLEN (p2_param);
}

size_t
cbdc_maxlen_nym (void)
{
  return CBDC_NYMLEN;
}

size_t
cbdc_maxlen_opening (void)
{
  return CBDC_OPENINGLEN;
}

size_t
cbdc_maxlen_userstate (void)
{
  return CBDC_USERSTATELEN;
}

size_t
cbdc_maxlen_sok (void)
{
  return CBDC_PROOFLEN (popen_param);
}

/* helpers */

static int
cbdc_redc16 (int x)
{
  if (x > 8)
    return x - 16;
  else if (x < -7)
    return x + 16;
  else
    return x;
}

/*
 * Encode n attributes of 256 bits as n polys: each attribute is represented
 * in base 2^4 by 64 coefficients in [-7,8].
 */
static void
cbdc_encattrs (polyvec_t v, const uint8_t *attrs, unsigned int n)
{
  int64_t coeffs[CBDC_NATTRS * CBDC_DEG];
  unsigned int i, j;

  for (i = 0; i < n; i++)
    for (j = 0; j < CBDC_ATTRLEN; j++)
      {
        const uint8_t b = attrs[i * CBDC_ATTRLEN + j];

        coeffs[i * CBDC_DEG + j * 2] = cbdc_redc16 (b / 16);
        coeffs[i * CBDC_DEG + j * 2 + 1] = cbdc_redc16 (b % 16);
      }
  polyvec_set_coeffvec_i64 (v, coeffs);
}

/* expand 64 bytes to 8 binary polys, msb first */
static void
cbdc_expand_tau (polyvec_t tau, const uint8_t bytes[64])
{
  int64_t coeffs[8 * CBDC_DEG];
  unsigned int k;

  for (k = 0; k < 8 * CBDC_DEG; k++)
    coeffs[k] = (bytes[k >> 3] >> (7 - (k & 7))) & 1;
  polyvec_set_coeffvec_i64 (tau, coeffs);
}

static int
cbdc_in (const unsigned int *idx, unsigned int n, unsigned int j)
{
  unsigned int i;

  for (i = 0; i < n; i++)
    if (idx[i] == j)
      return 1;
  return 0;
}

static int
cbdc_idx_valid (const unsigned int *idx, unsigned int n)
{
  unsigned int i;

  for (i = 0; i < n; i++)
    if (idx[i] >= CBDC_NATTRS)
      return 0;
  return 1;
}

/* dst[off+i] = src[i] */
static void
cbdc_put (polyvec_t dst, unsigned int off, polyvec_t src)
{
  unsigned int i, n = polyvec_get_nelems (src);

  for (i = 0; i < n; i++)
    polyvec_set_elem (dst, off + i, polyvec_get_elem (src, i));
}

/* dst[r0..,c0..] = +/- src */
static void
cbdc_place (polymat_t dst, polymat_t src, unsigned int r0, unsigned int c0,
            int neg)
{
  unsigned int i, j;
  const unsigned int nrows = polymat_get_nrows (src);
  const unsigned int ncols = polymat_get_ncols (src);

  for (i = 0; i < nrows; i++)
    for (j = 0; j < ncols; j++)
      {
        poly_ptr d = polymat_get_elem (dst, r0 + i, c0 + j);
        poly_ptr s = polymat_get_elem (src, i, j);

        if (neg)
          poly_neg (d, s);
        else
          poly_set (d, s);
      }
}

/* m_pub: m with unrevealed polys zeroed. m_priv: m with revealed zeroed. */
static void
cbdc_split (polyvec_t mpub, polyvec_t mpriv, polyvec_t m,
            const unsigned int *idx_rev, unsigned int nrev)
{
  unsigned int j;

  polyvec_alloc (mpub, cbdc_ring, CBDC_NATTRS);
  polyvec_alloc (mpriv, cbdc_ring, CBDC_NATTRS);
  polyvec_set (mpub, m);
  polyvec_set (mpriv, m);
  for (j = 0; j < CBDC_NATTRS; j++)
    {
      if (cbdc_in (idx_rev, nrev, j))
        poly_set_zero (polyvec_get_elem (mpriv, j));
      else
        poly_set_zero (polyvec_get_elem (mpub, j));
    }
}

/* AM with revealed columns zeroed */
static void
cbdc_AMpriv (polymat_t out, const unsigned int *idx_rev, unsigned int nrev)
{
  unsigned int i, j;

  polymat_alloc (out, cbdc_ring, 8, CBDC_NATTRS);
  polymat_set (out, cbdc_AM);
  for (j = 0; j < CBDC_NATTRS; j++)
    if (cbdc_in (idx_rev, nrev, j))
      for (i = 0; i < 8; i++)
        poly_set_zero (polymat_get_elem (out, i, j));
}

/* P1: A=[Ar,AMpriv] (8x21), u=-(t-AMpub*mpub) */
static void
cbdc_p1_statement (polymat_t A, polyvec_t u, polyvec_t t,
                   const unsigned int *idx_rev, unsigned int nrev,
                   polyvec_t m)
{
  polymat_t AMpriv, AM;
  polyvec_t mpub, mpriv, amm;

  cbdc_mat_copy (AM, cbdc_AM);
  cbdc_AMpriv (AMpriv, idx_rev, nrev);
  cbdc_split (mpub, mpriv, m, idx_rev, nrev);

  polymat_alloc (A, cbdc_ring, 8, 16 + CBDC_NATTRS);
  polymat_set_zero (A);
  cbdc_place (A, cbdc_AR, 0, 0, 0);
  cbdc_place (A, AMpriv, 0, 16, 0);

  polyvec_alloc (amm, cbdc_ring, 8);
  polyvec_mul (amm, AM, mpub);
  polyvec_alloc (u, cbdc_ring, 8);
  polyvec_sub (u, amm, t, 0);
  polyvec_redc (u, u);

  polyvec_free (amm);
  polyvec_free (mpub);
  polyvec_free (mpriv);
  polymat_free (AMpriv);
  polymat_free (AM);
}

/*
 * P2: [Ar,Atau,-B1,-B2,AMpriv,0]*(r,tau,s1,s2,mpriv,rcom)+(AMpub*mpub+c0,0)=0,
 * with, for each committed attribute idx_com[i] with commitment nyms[i], an
 * additional block of CBDC_COM_ROWS rows
 *   Acom[:,0]*attr[idx_com[i]] + Acom[:,1..10]*rcom_i - nym_i = 0,
 * A in Rp^((8+3*CBDC_COM_ROWS)x75), u in Rp^(8+3*CBDC_COM_ROWS).
 * Unused blocks are zero. Committed attributes are never revealed, hence their
 * columns in AMpriv are not zeroed and they stay part of mpriv.
 */
static void
cbdc_p2_statement (polymat_t A, polyvec_t u, polymat_t B2, polyvec_t c0,
                   polyvec_t m,
                   const unsigned int *idx_rev, unsigned int nrev,
                   polyvec_t *nyms, const unsigned int *idx_com,
                   unsigned int ncom)
{
  polymat_t AMpriv, AM, AR, ATAU, Acom;
  polyvec_t mpub, mpriv, amm;
  poly_t neg1;
  unsigned int i, j, k;

  cbdc_mat_copy (AM, cbdc_AM);
  cbdc_mat_copy (AR, cbdc_AR);
  cbdc_mat_copy (ATAU, cbdc_ATAU);
  cbdc_mat_copy (Acom, cbdc_Acom);
  cbdc_AMpriv (AMpriv, idx_rev, nrev);
  cbdc_split (mpub, mpriv, m, idx_rev, nrev);

  polymat_alloc (A, cbdc_ring, 8 + 3 * CBDC_COM_ROWS, 40 + 5 + 3 * 10);
  polymat_set_zero (A);
  cbdc_place (A, AR, 0, 0, 0);
  cbdc_place (A, ATAU, 0, 16, 0);
  /* -B1 = -identity */
  poly_alloc (neg1, cbdc_ring);
  poly_set_zero (neg1);
  int_set_i64 (poly_get_coeff (neg1, 0), -1);
  for (i = 0; i < 8; i++)
    polymat_set_elem (A, i, 24 + i, neg1);
  poly_free (neg1);
  cbdc_place (A, B2, 0, 32, 1);
  cbdc_place (A, AMpriv, 0, 40, 0);

  polyvec_alloc (amm, cbdc_ring, 8);
  polyvec_mul (amm, AM, mpub);
  polyvec_add (amm, amm, c0, 0);
  polyvec_redc (amm, amm);
  polyvec_alloc (u, cbdc_ring, 8 + 3 * CBDC_COM_ROWS);
  polyvec_set_zero (u);
  cbdc_put (u, 0, amm);

  /* commitment blocks */
  for (i = 0; i < ncom; i++)
    {
      const unsigned int row = 8 + i * CBDC_COM_ROWS;

      k = idx_com[i];
      for (j = 0; j < CBDC_COM_ROWS; j++)
        {
          unsigned int c;
          poly_ptr nu = polyvec_get_elem (u, row + j);

          polymat_set_elem (A, row + j, 40 + k, polymat_get_elem (Acom, j, 0));
          for (c = 0; c < 10; c++)
            polymat_set_elem (A, row + j, 45 + i * 10 + c,
                              polymat_get_elem (Acom, j, 1 + c));
          poly_neg (nu, polyvec_get_elem (nyms[i], j));
        }
    }
  polyvec_redc (u, u);

  polyvec_free (amm);
  polyvec_free (mpub);
  polyvec_free (mpriv);
  polymat_free (AMpriv);
  polymat_free (AM);
  polymat_free (AR);
  polymat_free (ATAU);
  polymat_free (Acom);
}

/* Falcon pubkey -> 8x8 matrix B2 over Rp s.t. B2*aut(s) = aut(pk*s) */
static void
cbdc_decode_pk (polymat_t B2, const uint8_t pubkey[897])
{
  int16_t h[512];
  polymat_t pk;
  poly_t p;

  falcon_decode_pubkey (h, pubkey);

  poly_alloc (p, cbdc_falcon_ring);
  poly_set_coeffvec_i16 (p, h);
  polymat_alloc (pk, cbdc_falcon_ring, 1, 1);
  polymat_set_elem (pk, 0, 0, p);

  polymat_alloc (B2, cbdc_ring, 8, 8);
  lin_toisoring (B2, NULL, pk, NULL);
  polymat_redc (B2, B2);

  polymat_free (pk);
  poly_free (p);
}

/*
 * Public non-zero target c0 in Rp^8, derived from the issuer public key. The
 * credential equation is Ar*r + Atau*tau + Am*m + c0 = s1 + B2*s2. Without c0
 * the P2 statement is homogeneous when no (or only zero) attributes are
 * revealed, and the zero witness satisfies it: anybody could present without
 * a credential.
 */
static void
cbdc_derive_c0 (polyvec_t c0, const uint8_t pubkey[897])
{
  static const char dom[] = "panurus/lazer/anoncreds/c0/v1";
  shake128_state_t hstate;
  uint8_t seed[32];
  INT_T (mod, 1);

  shake128_init (hstate);
  shake128_absorb (hstate, (const uint8_t *)dom, sizeof (dom) - 1);
  shake128_absorb (hstate, pubkey, 897);
  shake128_squeeze (hstate, seed, sizeof (seed));
  shake128_clear (hstate);

  int_set_i64 (mod, CBDC_MOD);
  polyvec_alloc (c0, cbdc_ring, 8);
  polyvec_urandom (c0, mod, CBDC_LOGMOD, seed, 0);
  polyvec_redc (c0, c0);
}

/*
 * Decode a credential tau,s1,s2 from a zero padded buffer (see cbdc_padded)
 * holding credlen bytes. Returns 1 if it decodes and consumes exactly credlen
 * bytes.
 */
static int
cbdc_dec_cred (polyvec_t tau, polyvec_t s1, polyvec_t s2,
               const uint8_t *credbuf, size_t credlen)
{
  coder_state_t cstate;
  uint8_t tau_[64];
  unsigned int off;

  coder_dec_begin (cstate, credbuf);
  if (coder_dec_bytes (cstate, tau_, sizeof (tau_)) != 0)
    return 0;
  coder_dec_grandom3 (cstate, s1, CBDC_LOG2O_BLINDSIG);
  coder_dec_grandom3 (cstate, s2, CBDC_LOG2O_BLINDSIG);
  if (coder_dec_end (cstate) != 1)
    return 0;
  off = coder_get_offset (cstate);
  if (off % 8 != 0 || (size_t)(off >> 3) != credlen)
    return 0;
  cbdc_expand_tau (tau, tau_);
  return 1;
}

void
cbdc_issuer_keygen (uint8_t sk[1281], uint8_t pk[897])
{
  falcon_keygen (sk, pk);
}

/* user */

void
cbdc_user_init (cbdc_user_state_t state, const uint8_t pubkey[897])
{
  cbdc_pp_init ();

  memset (state, 0, sizeof (cbdc_user_state_t));
  cbdc_decode_pk (state->B2, pubkey);
  cbdc_derive_c0 (state->c0, pubkey);
  lin_prover_init (state->p1, cbdc_p1pp, p1_param);
  lin_prover_init (state->p2, cbdc_p2pp, p2_param);
}

void
cbdc_user_clear (cbdc_user_state_t state)
{
  lin_prover_clear (state->p1);
  lin_prover_clear (state->p2);
  polymat_free (state->B2);
  polyvec_free (state->c0);
  if (state->masked)
    {
      polyvec_free (state->r);
      polyvec_free (state->m);
      state->masked = 0;
    }
}

/* encode a polyvec with coefficients in Z_12289 */
static size_t
cbdc_enc_vec (uint8_t *out, polyvec_t v)
{
  coder_state_t cstate;
  unsigned int len;
  INT_T (mod, 1);

  int_set_i64 (mod, CBDC_MOD);
  polyvec_redp (v, v);

  coder_enc_begin (cstate, out);
  coder_enc_urandom3 (cstate, v, mod, CBDC_LOGMOD);
  coder_enc_end (cstate);
  len = coder_get_offset (cstate);
  ASSERT_ERR (len % 8 == 0);
  return len >> 3;
}

/*
 * Decode a polyvec from the first inlen bytes of in, returns the number of
 * bytes read or -1. The decoder may read past inlen, so the input is copied
 * into a zero padded buffer first.
 */
static long
cbdc_dec_vec (polyvec_t v, const uint8_t *in, size_t inlen)
{
  coder_state_t cstate;
  unsigned int off;
  uint8_t buf[CBDC_USERSTATELEN + 64];
  INT_T (mod, 1);

  if (in == NULL || inlen > CBDC_USERSTATELEN)
    return -1;
  memset (buf, 0, sizeof (buf));
  memcpy (buf, in, inlen);

  int_set_i64 (mod, CBDC_MOD);

  coder_dec_begin (cstate, buf);
  if (coder_dec_urandom3 (cstate, v, mod, CBDC_LOGMOD) != 0)
    return -1;
  if (coder_dec_end (cstate) != 1)
    return -1;
  off = coder_get_offset (cstate);
  if (off % 8 != 0 || (size_t)(off >> 3) > inlen)
    return -1;
  polyvec_redc (v, v);
  return off >> 3;
}

/* zero padded copy of an untrusted input, NULL if len > maxlen. Free it. */
static uint8_t *
cbdc_padded (const uint8_t *in, size_t len, size_t maxlen)
{
  uint8_t *buf;

  if (len > maxlen || (len > 0 && in == NULL))
    return NULL;
  buf = calloc (1, maxlen + 64);
  if (buf == NULL)
    return NULL;
  if (len > 0)
    memcpy (buf, in, len);
  return buf;
}

/* l2(r)^2 bound of the commitment randomness, wl2 in anoncreds_p2_params.py */
#define CBDC_L2SQR_BND_COM (39 * 39)

/* nym = Acom*(attr,r), a is the encoding of attr */
static void
cbdc_commit_compute (cbdc_commit_t com, polyvec_t a)
{
  polyvec_t s;
  polymat_t Acom;

  cbdc_mat_copy (Acom, cbdc_Acom);
  polyvec_alloc (s, cbdc_ring, 1 + 10);
  cbdc_put (s, 0, a);
  cbdc_put (s, 1, com->r);

  polyvec_alloc (com->nym, cbdc_ring, CBDC_COM_ROWS);
  polyvec_mul (com->nym, Acom, s);
  polyvec_redc (com->nym, com->nym);

  polyvec_free (s);
  polymat_free (Acom);
}

/* encode nym to exactly CBDC_NYMLEN bytes */
static void
cbdc_nym_encode (uint8_t *nym, size_t *nymlen, polyvec_t nymv)
{
  /* encoding reduces to [0,q) in place, so encode a copy */
  polyvec_t tmp;
  size_t len;

  polyvec_alloc (tmp, cbdc_ring, CBDC_COM_ROWS);
  polyvec_set (tmp, nymv);
  len = cbdc_enc_vec (nym, tmp);
  ASSERT_ERR (len == CBDC_NYMLEN);
  *nymlen = len;
  polyvec_free (tmp);
}

/* nym = Acom*(attr,r) with internal coins for r */
void
cbdc_user_commit (cbdc_commit_t com, uint8_t *nym, size_t *nymlen,
                  const uint8_t attr[32])
{
  polyvec_t a;
  uint8_t seed[32];
  INT_T (lo, 1);
  INT_T (hi, 1);
  INT_T (l2sqr, 2);
  INT_T (bnd, 2);

  cbdc_pp_init ();

  int_set_i64 (lo, -2);
  int_set_i64 (hi, 2);
  int_set_i64 (bnd, CBDC_L2SQR_BND_COM);

  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);

  /* r is uniform in [-2,2], resample (fresh seed) until l2(r)^2 <= bound */
  polyvec_alloc (com->r, cbdc_ring, 10);
  do
    {
      bytes_urandom (seed, sizeof (seed));
      polyvec_urandom_bnd (com->r, lo, hi, seed, 0);
      polyvec_redc (com->r, com->r);
      polyvec_l2sqr (l2sqr, com->r);
    }
  while (int_gt (l2sqr, bnd));

  cbdc_commit_compute (com, a);
  cbdc_nym_encode (nym, nymlen, com->nym);

  polyvec_free (a);
}

void
cbdc_commit_clear (cbdc_commit_t com)
{
  polyvec_free (com->nym);
  polyvec_free (com->r);
}

/* encode the opening (the randomness r) of a commitment */
void
cbdc_commit_export (cbdc_commit_t com, uint8_t *opening, size_t *openinglen)
{
  polyvec_t tmp;

  polyvec_alloc (tmp, cbdc_ring, 10);
  polyvec_set (tmp, com->r);
  *openinglen = cbdc_enc_vec (opening, tmp);
  ASSERT_ERR (*openinglen == CBDC_OPENINGLEN);
  polyvec_free (tmp);
}

/* l2(r)^2 <= bound and r decodes. Returns 1 if the opening is valid. */
static int
cbdc_opening_decode (polyvec_t r, const uint8_t *opening, size_t openinglen)
{
  INT_T (l2sqr, 2);
  INT_T (bnd, 2);

  if (openinglen != CBDC_OPENINGLEN)
    return 0;
  if (cbdc_dec_vec (r, opening, openinglen) < 0)
    return 0;
  int_set_i64 (bnd, CBDC_L2SQR_BND_COM);
  polyvec_l2sqr (l2sqr, r);
  return !int_gt (l2sqr, bnd);
}

/*
 * Rebuild a commitment to attr from its opening. Returns 1 on success, in
 * which case com must be cleared by the caller.
 */
int
cbdc_commit_import (cbdc_commit_t com, uint8_t *nym, size_t *nymlen,
                    const uint8_t attr[32], const uint8_t *opening,
                    size_t openinglen)
{
  polyvec_t a;

  cbdc_pp_init ();

  polyvec_alloc (com->r, cbdc_ring, 10);
  if (!cbdc_opening_decode (com->r, opening, openinglen))
    {
      polyvec_free (com->r);
      return 0;
    }
  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  cbdc_commit_compute (com, a);
  cbdc_nym_encode (nym, nymlen, com->nym);
  polyvec_free (a);
  return 1;
}

/* Returns 1 if nym = Acom*(attr,r) for the r in opening. */
int
cbdc_opening_verify (const uint8_t *nym, size_t nymlen, const uint8_t attr[32],
                     const uint8_t *opening, size_t openinglen)
{
  cbdc_commit_t com;
  polyvec_t nymv, a;
  int b = 0;

  cbdc_pp_init ();

  if (nymlen != CBDC_NYMLEN)
    return 0;
  polyvec_alloc (nymv, cbdc_ring, CBDC_COM_ROWS);
  if (cbdc_dec_vec (nymv, nym, nymlen) != (long)CBDC_NYMLEN)
    {
      polyvec_free (nymv);
      return 0;
    }
  polyvec_alloc (com->r, cbdc_ring, 10);
  if (cbdc_opening_decode (com->r, opening, openinglen))
    {
      polyvec_alloc (a, cbdc_ring, 1);
      cbdc_encattrs (a, attr, 1);
      cbdc_commit_compute (com, a);
      polyvec_redc (nymv, nymv);
      b = polyvec_eq (com->nym, nymv);
      polyvec_free (com->nym);
      polyvec_free (a);
    }
  polyvec_free (com->r);
  polyvec_free (nymv);
  return b == 1;
}

/* statement for the SoK: Acom*(attr,r) - nym = 0 */
static void
cbdc_sok_statement (polyvec_t t, polyvec_t nym)
{
  polyvec_alloc (t, cbdc_ring, CBDC_COM_ROWS);
  polyvec_set (t, nym);
  polyvec_neg_self (t);
  polyvec_redc (t, t);
}

/*
 * Signature of knowledge of the opening of com (attr,r), bound to the
 * context ctx (e.g. a domain separator and the message).
 */
void
cbdc_user_sokprove (uint8_t *proof, size_t *proofl, cbdc_commit_t com,
                    const uint8_t *ctx, size_t ctxlen, const uint8_t attr[32])
{
  lin_prover_state_t prover;
  polyvec_t a, s, t;
  polymat_t Acom;

  cbdc_pp_init ();

  cbdc_mat_copy (Acom, cbdc_Acom);
  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  polyvec_alloc (s, cbdc_ring, 1 + 10);
  cbdc_put (s, 0, a);
  cbdc_put (s, 1, com->r);
  cbdc_sok_statement (t, com->nym);

  lin_prover_init (prover, cbdc_popenpp, popen_param);
  lin_prover_set_statement (prover, Acom, t);
  lin_prover_set_witness (prover, s);
  lin_prover_prove_ctx (prover, proof, proofl, NULL, ctx, ctxlen);
  lin_prover_clear (prover);

  polyvec_free (a);
  polyvec_free (s);
  polyvec_free (t);
  polymat_free (Acom);
}

/* attrs: 5*32 bytes */
void
cbdc_user_maskattrs (cbdc_user_state_t state, uint8_t *masked_attrs,
                     size_t *masked_attrslen, const uint8_t *attrs,
                     const unsigned int *idx_rev, unsigned int nrev)
{
  polyvec_t m, r, t, tmp, u, w, mpub, mpriv;
  polymat_t A;
  uint8_t seed[32];
  uint8_t dom = 0;
  size_t tlen, plen;
  INT_T (l2sqr, 2);
  INT_T (bnd, 2);

  cbdc_pp_init ();

  if (state->masked)
    {
      polyvec_free (state->r);
      polyvec_free (state->m);
      state->masked = 0;
    }

  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs, CBDC_NATTRS);

  /* r: gaussian with sigma = 1.55*2^1 and l2(r)^2 <= bound */
  int_set_i64 (bnd, CBDC_L2SQR_BND_R);
  polyvec_alloc (r, cbdc_ring, 16);
  do
    {
      /* fresh seed per attempt, a single seed can keep exceeding the bound */
      bytes_urandom (seed, sizeof (seed));
      polyvec_grandom (r, 1, seed, dom);
      polyvec_redc (r, r);
      polyvec_l2sqr (l2sqr, r);
    }
  while (int_gt (l2sqr, bnd));

  /* t = Ar*r + Am*m */
  polyvec_alloc (t, cbdc_ring, 8);
  polyvec_alloc (tmp, cbdc_ring, 8);
  {
    polymat_t AR, AM;

    cbdc_mat_copy (AR, cbdc_AR);
    cbdc_mat_copy (AM, cbdc_AM);
    polyvec_mul (t, AR, r);
    polyvec_mul (tmp, AM, m);
    polymat_free (AR);
    polymat_free (AM);
  }
  polyvec_add (t, t, tmp, 0);
  polyvec_redc (t, t);

  cbdc_p1_statement (A, u, t, idx_rev, nrev, m);

  polyvec_alloc (w, cbdc_ring, 16 + CBDC_NATTRS);
  cbdc_split (mpub, mpriv, m, idx_rev, nrev);
  cbdc_put (w, 0, r);
  cbdc_put (w, 16, mpriv);

  lin_prover_set_statement (state->p1, A, u);
  lin_prover_set_witness (state->p1, w);

  /* masked attrs = enc(t) || P1 */
  tlen = cbdc_enc_vec (masked_attrs, t);
  lin_prover_prove (state->p1, masked_attrs + tlen, &plen, NULL);
  *masked_attrslen = tlen + plen;

  /* save randomness and message */
  state->r[0] = r[0];
  state->m[0] = m[0];
  state->masked = 1;

  polyvec_free (t);
  polyvec_free (tmp);
  polyvec_free (u);
  polyvec_free (w);
  polyvec_free (mpub);
  polyvec_free (mpriv);
  polymat_free (A);
}

/*
 * Ar*r + Atau*tau + Am*m + c0 = s1 + B2*s2 and l2(s1,s2) is bounded, for the
 * r and m in state. Returns 1 if the credential is valid.
 */
static int
cbdc_cred_ok (cbdc_user_state_t state, polyvec_t tau, polyvec_t s1,
              polyvec_t s2)
{
  polyvec_t acc, tmp, zero;
  polymat_t AR, ATAU, AM, B2;
  int rv;
  INT_T (l2sqr, 2);
  INT_T (l2sqr2, 2);
  INT_T (bnd, 2);

  /* norm bound of the preimage, sqrt(34034726) in anoncreds_p2_params.py */
  polyvec_l2sqr (l2sqr, s1);
  polyvec_l2sqr (l2sqr2, s2);
  int_add (l2sqr, l2sqr, l2sqr2);
  int_set_i64 (bnd, 34034726);
  if (int_gt (l2sqr, bnd))
    return 0;

  cbdc_mat_copy (AR, cbdc_AR);
  cbdc_mat_copy (ATAU, cbdc_ATAU);
  cbdc_mat_copy (AM, cbdc_AM);
  cbdc_mat_copy (B2, state->B2);

  polyvec_alloc (acc, cbdc_ring, 8);
  polyvec_alloc (tmp, cbdc_ring, 8);
  polyvec_alloc (zero, cbdc_ring, 8);
  polyvec_set_zero (zero);

  polyvec_mul (acc, AR, state->r);
  polyvec_mul (tmp, ATAU, tau);
  polyvec_add (acc, acc, tmp, 0);
  polyvec_mul (tmp, AM, state->m);
  polyvec_add (acc, acc, tmp, 0);
  polyvec_add (acc, acc, state->c0, 0);
  polyvec_sub (acc, acc, s1, 0);
  polyvec_mul (tmp, B2, s2);
  polyvec_sub (acc, acc, tmp, 0);
  polyvec_redc (acc, acc);
  rv = polyvec_eq (acc, zero) == 1;

  polyvec_free (acc);
  polyvec_free (tmp);
  polyvec_free (zero);
  polymat_free (AR);
  polymat_free (ATAU);
  polymat_free (AM);
  polymat_free (B2);
  return rv;
}

/* Returns 1 if com->nym = Acom*(m_k,com->r) for the encoded attribute m_k. */
static int
cbdc_com_ok (cbdc_commit_ptr com, poly_ptr mk)
{
  cbdc_commit_t chk;
  polyvec_t a;
  int b;

  polyvec_alloc (a, cbdc_ring, 1);
  poly_set (polyvec_get_elem (a, 0), mk);
  chk->r[0] = com->r[0];
  cbdc_commit_compute (chk, a);
  b = polyvec_eq (chk->nym, com->nym) == 1;
  polyvec_free (chk->nym);
  polyvec_free (a);
  return b;
}

/* committed indices must be distinct, valid, disjoint from the revealed ones */
static int
cbdc_com_valid (const unsigned int *idx_rev, unsigned int nrev,
                const unsigned int *idx_com, unsigned int ncom)
{
  unsigned int i, j;

  if (ncom > CBDC_MAXCOM || !cbdc_idx_valid (idx_rev, nrev)
      || !cbdc_idx_valid (idx_com, ncom))
    return 0;
  for (i = 0; i < ncom; i++)
    {
      if (cbdc_in (idx_rev, nrev, idx_com[i]))
        return 0;
      for (j = 0; j < i; j++)
        if (idx_com[j] == idx_com[i])
          return 0;
    }
  return 1;
}

/*
 * coms[i] is the commitment to attribute idx_com[i]. The presentation is
 * bound to ctx. Returns 1 on success.
 */
int
cbdc_user_presgen (cbdc_user_state_t state, uint8_t *pres, size_t *preslen,
                   const uint8_t *cred, size_t credlen,
                   const unsigned int *idx_rev, unsigned int nrev,
                   cbdc_commit_ptr *coms, const unsigned int *idx_com,
                   unsigned int ncom, const uint8_t *ctx, size_t ctxlen)
{
  polyvec_t tau, s1, s2, w, u;
  polyvec_t nyms[CBDC_MAXCOM];
  polymat_t A;
  uint8_t *credbuf;
  unsigned int i;
  int rv = 0;

  cbdc_pp_init ();

  if (!state->masked || !cbdc_com_valid (idx_rev, nrev, idx_com, ncom))
    return 0;
  credbuf = cbdc_padded (cred, credlen, cbdc_maxlen_cred ());
  if (credbuf == NULL)
    return 0;

  polyvec_alloc (tau, cbdc_ring, 8);
  polyvec_alloc (s1, cbdc_ring, 8);
  polyvec_alloc (s2, cbdc_ring, 8);

  /*
   * decode blindsig tau,s1,s2 and check the whole witness: the prover must
   * not be run on a false statement (it does not terminate cleanly then).
   */
  if (!cbdc_dec_cred (tau, s1, s2, credbuf, credlen)
      || !cbdc_cred_ok (state, tau, s1, s2))
    goto err_dec;
  for (i = 0; i < ncom; i++)
    if (coms[i] == NULL
        || !cbdc_com_ok (coms[i], polyvec_get_elem (state->m, idx_com[i])))
      goto err_dec;

  for (i = 0; i < ncom; i++)
    nyms[i][0] = coms[i]->nym[0];
  cbdc_p2_statement (A, u, state->B2, state->c0, state->m, idx_rev, nrev, nyms,
                     idx_com, ncom);

  /* w = (r,tau,s1,s2,mpriv,rcom) */
  {
    polyvec_t mpub, mpriv;

    cbdc_split (mpub, mpriv, state->m, idx_rev, nrev);
    polyvec_alloc (w, cbdc_ring, 40 + 5 + 3 * 10);
    polyvec_set_zero (w);
    cbdc_put (w, 0, state->r);
    cbdc_put (w, 16, tau);
    cbdc_put (w, 24, s1);
    cbdc_put (w, 32, s2);
    cbdc_put (w, 40, mpriv);
    for (i = 0; i < ncom; i++)
      {
        cbdc_put (w, 45 + i * 10, coms[i]->r);
      }
    polyvec_free (mpub);
    polyvec_free (mpriv);
  }

  lin_prover_set_statement (state->p2, A, u);
  lin_prover_set_witness (state->p2, w);
  lin_prover_prove_ctx (state->p2, pres, preslen, NULL, ctx, ctxlen);
  rv = 1;

  polyvec_free (w);
  polyvec_free (u);
  polymat_free (A);
err_dec:
  polyvec_free (tau);
  polyvec_free (s1);
  polyvec_free (s2);
  free (credbuf);
  return rv;
}

/*
 * Cheap check of a credential against the state of the user (after maskattrs
 * or import): Ar*r + Atau*tau + Am*m + c0 = s1 + B2*s2 and l2(s1,s2) is bounded.
 * Returns 1 if the credential is valid.
 */
int
cbdc_user_credverify (cbdc_user_state_t state, const uint8_t *cred,
                      size_t credlen)
{
  polyvec_t tau, s1, s2;
  uint8_t *credbuf;
  int rv = 0;

  cbdc_pp_init ();

  if (!state->masked)
    return 0;
  credbuf = cbdc_padded (cred, credlen, cbdc_maxlen_cred ());
  if (credbuf == NULL)
    return 0;

  polyvec_alloc (tau, cbdc_ring, 8);
  polyvec_alloc (s1, cbdc_ring, 8);
  polyvec_alloc (s2, cbdc_ring, 8);

  if (!cbdc_dec_cred (tau, s1, s2, credbuf, credlen))
    goto err_dec;

  rv = cbdc_cred_ok (state, tau, s1, s2);

err_dec:
  polyvec_free (tau);
  polyvec_free (s1);
  polyvec_free (s2);
  free (credbuf);
  return rv;
}

/* user state import/export: only the masking randomness r is secret */
void
cbdc_user_export (cbdc_user_state_t state, uint8_t *out, size_t *outlen)
{
  polyvec_t tmp;

  ASSERT_ERR (state->masked);
  polyvec_alloc (tmp, cbdc_ring, 16);
  polyvec_set (tmp, state->r);
  *outlen = cbdc_enc_vec (out, tmp);
  ASSERT_ERR (*outlen == CBDC_USERSTATELEN);
  polyvec_free (tmp);
}

/* attrs: 5*32 bytes. Returns 1 on success. */
int
cbdc_user_import (cbdc_user_state_t state, const uint8_t *in, size_t inlen,
                  const uint8_t *attrs)
{
  polyvec_t r, m;
  INT_T (l2sqr, 2);
  INT_T (bnd, 2);

  cbdc_pp_init ();

  if (inlen != CBDC_USERSTATELEN)
    return 0;
  polyvec_alloc (r, cbdc_ring, 16);
  if (cbdc_dec_vec (r, in, inlen) < 0)
    {
      polyvec_free (r);
      return 0;
    }
  int_set_i64 (bnd, CBDC_L2SQR_BND_R);
  polyvec_l2sqr (l2sqr, r);
  if (int_gt (l2sqr, bnd))
    {
      polyvec_free (r);
      return 0;
    }
  if (state->masked)
    {
      polyvec_free (state->r);
      polyvec_free (state->m);
    }
  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs, CBDC_NATTRS);
  state->r[0] = r[0];
  state->m[0] = m[0];
  state->masked = 1;
  return 1;
}

/* Returns 1 if the attributes satisfy the P1 norm bound l2(enc(attrs)) <= 91. */
int
cbdc_attrs_ok (const uint8_t *attrs)
{
  polyvec_t m;
  INT_T (l2sqr, 2);
  INT_T (bnd, 2);
  int b;

  cbdc_pp_init ();

  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs, CBDC_NATTRS);
  int_set_i64 (bnd, 91 * 91);
  polyvec_l2sqr (l2sqr, m);
  b = !int_gt (l2sqr, bnd);
  polyvec_free (m);
  return b;
}

/* issuer */

void
cbdc_issuer_init (cbdc_issuer_state_t state, const uint8_t pubkey[897],
                  const uint8_t privkey[1281])
{
  cbdc_pp_init ();

  memset (state, 0, sizeof (cbdc_issuer_state_t));
  memcpy (state->sk, privkey, sizeof (state->sk));
  cbdc_derive_c0 (state->c0, pubkey);
  lin_verifier_init (state->p1, cbdc_p1pp, p1_param);
}

void
cbdc_issuer_clear (cbdc_issuer_state_t state)
{
  lin_verifier_clear (state->p1);
  polyvec_free (state->c0);
}

/*
 * attrs_rev: 5*32 bytes, unrevealed attributes must be zero. Returns 1 if the
 * proof verifies and a credential was output.
 */
int
cbdc_issuer_issue (cbdc_issuer_state_t state, uint8_t *cred, size_t *credlen,
                   const uint8_t *attrs_rev, const unsigned int *idx_rev,
                   unsigned int nrev, const uint8_t *masked_attrs,
                   size_t masked_attrslen)
{
  polyvec_t m, t, u, tau, tmp, tp, s1v, s2v;
  polymat_t A, ATAU;
  uint8_t *masked = NULL;
  poly_t fp;
  int16_t tf[512], s1[512], s2[512];
  int64_t c[512];
  uint8_t tau_[64];
  coder_state_t cstate;
  long tlen;
  size_t len = 0, off;
  unsigned int i;
  int b = 0;

  cbdc_pp_init ();

  if (!cbdc_idx_valid (idx_rev, nrev))
    return 0;

  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs_rev, CBDC_NATTRS);

  /* decode masked attributes */
  masked = cbdc_padded (masked_attrs, masked_attrslen,
                        cbdc_maxlen_masked_attrs ());
  polyvec_alloc (t, cbdc_ring, 8);
  if (masked == NULL)
    goto err_t;
  tlen = cbdc_dec_vec (t, masked, masked_attrslen < 1024 ? masked_attrslen : 1024);
  if (tlen < 0)
    goto err_t;
  off = (size_t)tlen;

  /* verify proof for PoK(w): Aw + u = 0 */
  cbdc_p1_statement (A, u, t, idx_rev, nrev, m);
  lin_verifier_set_statement (state->p1, A, u);
  b = lin_verifier_verify (state->p1, masked + off, &len);
  polyvec_free (u);
  polymat_free (A);
  if (b != 1 || off + len != masked_attrslen)
    {
      b = 0;
      goto err_t;
    }

  /* internal coins */
  bytes_urandom (tau_, sizeof (tau_));
  polyvec_alloc (tau, cbdc_ring, 8);
  cbdc_expand_tau (tau, tau_);

  /* preimage sampling for Atau*tau + t + c0 */
  polyvec_alloc (tmp, cbdc_ring, 8);
  polyvec_alloc (tp, cbdc_ring, 8);
  cbdc_mat_copy (ATAU, cbdc_ATAU);
  polyvec_mul (tmp, ATAU, tau);
  polymat_free (ATAU);
  polyvec_add (tp, tmp, t, 0);
  polyvec_add (tp, tp, state->c0, 0);
  polyvec_redc (tp, tp);

  poly_alloc (fp, cbdc_falcon_ring);
  poly_fromisoring (fp, tp);
  poly_get_coeffvec_i64 (c, fp);
  for (i = 0; i < 512; i++)
    tf[i] = (int16_t)c[i];
  falcon_preimage_sample (s1, s2, tf, state->sk);

  polyvec_alloc (s1v, cbdc_ring, 8);
  polyvec_alloc (s2v, cbdc_ring, 8);
  poly_set_coeffvec_i16 (fp, s1);
  poly_toisoring (s1v, fp);
  poly_set_coeffvec_i16 (fp, s2);
  poly_toisoring (s2v, fp);

  /* encode blindsig tau,s1,s2 */
  coder_enc_begin (cstate, cred);
  coder_enc_bytes (cstate, tau_, sizeof (tau_));
  coder_enc_grandom3 (cstate, s1v, CBDC_LOG2O_BLINDSIG);
  coder_enc_grandom3 (cstate, s2v, CBDC_LOG2O_BLINDSIG);
  coder_enc_end (cstate);
  len = coder_get_offset (cstate);
  ASSERT_ERR (len % 8 == 0);
  *credlen = len >> 3;
  b = 1;

  poly_free (fp);
  polyvec_free (tau);
  polyvec_free (tmp);
  polyvec_free (tp);
  polyvec_free (s1v);
  polyvec_free (s2v);
err_t:
  free (masked);
  polyvec_free (t);
  polyvec_free (m);
  return b;
}

/* verifier */

void
cbdc_verifier_init (cbdc_verifier_state_t state, const uint8_t pubkey[897])
{
  cbdc_pp_init ();

  memset (state, 0, sizeof (cbdc_verifier_state_t));
  cbdc_decode_pk (state->B2, pubkey);
  cbdc_derive_c0 (state->c0, pubkey);
  lin_verifier_init (state->p2, cbdc_p2pp, p2_param);
}

void
cbdc_verifier_clear (cbdc_verifier_state_t state)
{
  lin_verifier_clear (state->p2);
  polymat_free (state->B2);
  polyvec_free (state->c0);
}

/*
 * attrs_rev: 5*32 bytes, unrevealed attributes must be zero. nyms[i] (of
 * nymlens[i] bytes) is the commitment to attribute idx_com[i]. The caller
 * must serialize access to the verifier state.
 */
int
cbdc_verifier_presver (cbdc_verifier_state_t state, const uint8_t *attrs_rev,
                       const unsigned int *idx_rev, unsigned int nrev,
                       const uint8_t *const *nyms, const size_t *nymlens,
                       const unsigned int *idx_com, unsigned int ncom,
                       const uint8_t *pres, size_t preslen,
                       const uint8_t *ctx, size_t ctxlen)
{
  polyvec_t m, u;
  polyvec_t nymv[CBDC_MAXCOM];
  polymat_t A;
  uint8_t *presbuf;
  size_t len = 0;
  unsigned int i, nok = 0;
  int b = 0;

  cbdc_pp_init ();

  if (!cbdc_com_valid (idx_rev, nrev, idx_com, ncom))
    return 0;
  presbuf = cbdc_padded (pres, preslen, cbdc_maxlen_pres ());
  if (presbuf == NULL)
    return 0;
  for (i = 0; i < ncom; i++)
    {
      polyvec_alloc (nymv[i], cbdc_ring, CBDC_COM_ROWS);
      nok++;
      if (nymlens[i] != CBDC_NYMLEN
          || cbdc_dec_vec (nymv[i], nyms[i], nymlens[i]) != (long)CBDC_NYMLEN)
        goto out;
    }

  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs_rev, CBDC_NATTRS);

  cbdc_p2_statement (A, u, state->B2, state->c0, m, idx_rev, nrev, nymv,
                     idx_com, ncom);
  lin_verifier_set_statement (state->p2, A, u);
  b = lin_verifier_verify_ctx (state->p2, presbuf, &len, ctx, ctxlen);
  if (b == 1 && len != preslen)
    b = 0;

  polyvec_free (u);
  polymat_free (A);
  polyvec_free (m);
out:
  for (i = 0; i < nok; i++)
    polyvec_free (nymv[i]);
  free (presbuf);
  return b == 1;
}

int
cbdc_verifier_sokverify (const uint8_t *nym, size_t nymlen,
                         const uint8_t *ctx, size_t ctxlen,
                         const uint8_t *proof, size_t proofl)
{
  lin_verifier_state_t verifier;
  polyvec_t nymv, t;
  polymat_t Acom;
  uint8_t *proofbuf;
  size_t len = 0;
  int b = 0;

  cbdc_pp_init ();

  if (nymlen != CBDC_NYMLEN)
    return 0;
  proofbuf = cbdc_padded (proof, proofl, cbdc_maxlen_sok ());
  if (proofbuf == NULL)
    return 0;

  polyvec_alloc (nymv, cbdc_ring, CBDC_COM_ROWS);
  if (cbdc_dec_vec (nymv, nym, nymlen) != (long)CBDC_NYMLEN)
    {
      polyvec_free (nymv);
      free (proofbuf);
      return 0;
    }
  cbdc_sok_statement (t, nymv);
  cbdc_mat_copy (Acom, cbdc_Acom);

  lin_verifier_init (verifier, cbdc_popenpp, popen_param);
  lin_verifier_set_statement (verifier, Acom, t);
  b = lin_verifier_verify_ctx (verifier, proofbuf, &len, ctx, ctxlen);
  if (b == 1 && len != proofl)
    b = 0;
  lin_verifier_clear (verifier);

  polymat_free (Acom);
  polyvec_free (t);
  polyvec_free (nymv);
  free (proofbuf);
  return b == 1;
}
