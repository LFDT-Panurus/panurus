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

#define CBDC_LOG2O_BLINDSIG 7 /* ceil(log(165/1.55,2)) */

/*
 * Overwrite n bytes at p with zeros in a way the compiler cannot elide
 * (explicit_bzero is not available everywhere and lazer #defines it to bzero
 * on macOS).
 */
static void
cbdc_wipe (void *p, size_t n)
{
  volatile uint8_t *q = (volatile uint8_t *)p;

  while (n--)
    *q++ = 0;
}

/* wipe and free a polyvec holding secret data */
static void
cbdc_vec_free_secret (polyvec_t v)
{
  polyvec_wipe (v);
  polyvec_free (v);
}

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
static uint8_t cbdc_blindsigpp[32];
static uint8_t cbdc_p1pp[32];
static uint8_t cbdc_p2pp[32];
static uint8_t cbdc_popenpp[32];
static polymat_t cbdc_AR;   /* 8x16 */
static polymat_t cbdc_AM;   /* 8x5 */
static polymat_t cbdc_ATAU; /* 8x8 */
static polymat_t cbdc_Acom; /* CBDC_COM_ROWSx11 commitment key */
/*
 * public matrices of the three proof systems, expanded once and in the CRT
 * domain (lazer patch 0011): every prover and verifier borrows them and only
 * reads them, so they are shared by all threads, for the process lifetime
 */
static lin_pp_t cbdc_p1lpp;
static lin_pp_t cbdc_p2lpp;
static lin_pp_t cbdc_popenlpp;

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
 * The public matrices (cbdc_AR, cbdc_AM, cbdc_ATAU, cbdc_Acom and the shared
 * lin_pp_t below) are read-only after initialization. Operations that convert
 * their operands in place (polyvec_mul, lin_*_set_statement) must work on a
 * private copy, see cbdc_mat_copy. The same holds for the state of a user,
 * which is shared by concurrent presentations.
 */

/*
 * Euclidean norm group i of params covers the n witness elements
 * start..start+n-1 and has the squared bound bnd.
 */
static int
cbdc_l2group_ok (const lin_params_struct *params, unsigned int i,
                 unsigned int start, unsigned int n, int64_t bnd)
{
  lnp_tbox_params_srcptr tbox = params->tbox_params;
  unsigned int j;

  if (i >= tbox->Z || tbox->n[i] != n || params->Es_nrows[i] != n)
    return 0;
  for (j = 0; j < n; j++)
    if (params->Es[i][j] != start + j)
      return 0;
  return int_get_i64 (tbox->l2Bsqr[i]) == bnd;
}

/* the common structure of the three proof systems */
static int
cbdc_lin_ok (const lin_params_struct *params, unsigned int nprime,
             unsigned int nwitness, unsigned int ngroups, unsigned int nbin)
{
  lnp_tbox_params_srcptr tbox = params->tbox_params;

  return params->dprime == CBDC_DEG && int_get_i64 (params->p) == CBDC_MOD
         && params->k == 1 && tbox->nprime == nprime
         && params->ns1_indices == nwitness && params->nm_indices == 0
         && tbox->Z == ngroups && tbox->nbin == nbin
         && params->Ps_nrows == nbin;
}

/*
 * Check the generated parameters (anoncreds_params.h) against the constants
 * of anoncreds.h and the witness layout used in this file, which must agree
 * or the statements would be unsound (or the prover would not terminate):
 * - P1 proves (r, mpriv) with [Ar, AMpriv] (8 rows): l2(r)^2 <= 109^2 and
 *   l2(m)^2 <= 91^2;
 * - P2 proves (r, tau, s1, s2, mpriv, rcom) (75 elements, 8 rows plus one
 *   block of CBDC_COM_ROWS rows for each of CBDC_MAXCOM commitments): r as
 *   in P1, tau binary, l2(s1,s2)^2 <= 34034726, each attribute <= 64^2 and
 *   each commitment randomness <= 39^2;
 * - the SoK proves (attr, r) with Acom (CBDC_COM_ROWS rows): 64^2 and 39^2.
 * Returns 0 if they agree, else the number of the first failed check.
 */
int
cbdc_params_check (void)
{
  unsigned int i;

  /* P1 */
  if (!cbdc_lin_ok (p1_param, 8, 16 + CBDC_NATTRS, 2, 0))
    return 1;
  if (!cbdc_l2group_ok (p1_param, 0, 0, 16, CBDC_L2SQR_BND_R))
    return 2;
  if (!cbdc_l2group_ok (p1_param, 1, 16, CBDC_NATTRS, CBDC_L2SQR_BND_ATTRS))
    return 3;

  /* P2 */
  if (!cbdc_lin_ok (p2_param, 8 + CBDC_MAXCOM * CBDC_COM_ROWS,
                    40 + CBDC_NATTRS + CBDC_MAXCOM * 10,
                    2 + CBDC_NATTRS + CBDC_MAXCOM, 8))
    return 4;
  for (i = 0; i < 8; i++)
    if (p2_param->Ps[i] != 16 + i) /* tau is binary */
      return 5;
  if (!cbdc_l2group_ok (p2_param, 0, 0, 16, CBDC_L2SQR_BND_R))
    return 6;
  if (!cbdc_l2group_ok (p2_param, 1, 24, 16, CBDC_L2SQR_BND_CRED))
    return 7;
  for (i = 0; i < CBDC_NATTRS; i++)
    if (!cbdc_l2group_ok (p2_param, 2 + i, 40 + i, 1, CBDC_L2SQR_BND_ATTR))
      return 8;
  for (i = 0; i < CBDC_MAXCOM; i++)
    if (!cbdc_l2group_ok (p2_param, 2 + CBDC_NATTRS + i,
                          40 + CBDC_NATTRS + 10 * i, 10, CBDC_L2SQR_BND_COM))
      return 9;

  /* SoK */
  if (!cbdc_lin_ok (popen_param, CBDC_COM_ROWS, 1 + 10, 2, 0))
    return 10;
  if (!cbdc_l2group_ok (popen_param, 0, 0, 1, CBDC_L2SQR_BND_ATTR))
    return 11;
  if (!cbdc_l2group_ok (popen_param, 1, 1, 10, CBDC_L2SQR_BND_COM))
    return 12;

  /* an attribute encodes into 64 coefficients in [-7,8] */
  if (CBDC_DEG * 8 * 8 > CBDC_L2SQR_BND_ATTR)
    return 13;
  return 0;
}

/* Returns 1 if the generated parameters agree with anoncreds.h, see above. */
int
cbdc_params_ok (void)
{
  return cbdc_params_check () == 0;
}

static void
cbdc_pp_do_init (void)
{
  lazer_init ();

  if (!cbdc_params_ok ())
    {
      /* a build with inconsistent parameters would produce unsound statements */
      fprintf (stderr, "cbdc: parameters do not match anoncreds.h (check %d)\n",
               cbdc_params_check ());
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

  lin_pp_init (cbdc_p1lpp, cbdc_p1pp, p1_param);
  lin_pp_init (cbdc_p2lpp, cbdc_p2pp, p2_param);
  lin_pp_init (cbdc_popenlpp, cbdc_popenpp, popen_param);
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

/*
 * Proof buffers hold 1.2 times the nominal proof length. The prover never
 * writes past them (lin_prover_prove_ctx_bnd); a proof that would not fit,
 * which the rejection sampling makes very unlikely, is discarded and proven
 * again with fresh coins, up to CBDC_PROVE_TRIES times.
 */
#define CBDC_PROOFLEN(params) ((size_t)lin_params_get_prooflen (params) * 6 / 5 + 1)
#define CBDC_PROVE_TRIES 4

/* prove into at most cap bytes, retrying with fresh coins. Returns 1 on success. */
static int
cbdc_prove (lin_prover_state_t prover, uint8_t *out, size_t cap, size_t *len,
            const uint8_t *ctx, size_t ctxlen)
{
  int i;

  for (i = 0; i < CBDC_PROVE_TRIES; i++)
    if (lin_prover_prove_ctx_bnd (prover, out, cap, len, NULL, ctx, ctxlen)
        == 1)
      return 1;
  *len = 0;
  return 0;
}

/*
 * prove into at most cap bytes, with the given coins (tests) or fresh ones,
 * retrying with fresh coins as cbdc_prove. Returns 1 on success.
 */
static int
cbdc_prove_coins (lin_prover_state_t prover, uint8_t *out, size_t cap,
                  size_t *len, const uint8_t *ctx, size_t ctxlen,
                  const uint8_t *coins)
{
  if (coins == NULL)
    return cbdc_prove (prover, out, cap, len, ctx, ctxlen);
  if (lin_prover_prove_ctx_bnd (prover, out, cap, len, coins, ctx, ctxlen)
      == 1)
    return 1;
  *len = 0;
  return 0;
}

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
  cbdc_wipe (coeffs, sizeof (coeffs));
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
  cbdc_wipe (coeffs, sizeof (coeffs));
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
  cbdc_vec_free_secret (mpub);
  cbdc_vec_free_secret (mpriv);
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
  cbdc_vec_free_secret (mpub);
  cbdc_vec_free_secret (mpriv);
  polymat_free (AMpriv);
  polymat_free (AM);
  polymat_free (AR);
  polymat_free (ATAU);
  polymat_free (Acom);
}

/*
 * Returns 1 if pubkey is a well-formed Falcon-512 public key (header byte and
 * all 512 coefficients < q), without exiting on malformed input.
 */
int
cbdc_pubkey_ok (const uint8_t pubkey[897])
{
  int16_t h[512];

  if (pubkey == NULL)
    return 0;
  return falcon_decode_pubkey_chk (h, pubkey) == 0;
}

/*
 * Falcon pubkey -> 8x8 matrix B2 over Rp s.t. B2*aut(s) = aut(pk*s). Returns
 * 1 on success, 0 (and allocates nothing) if pubkey is malformed.
 */
static int
cbdc_decode_pk (polymat_t B2, const uint8_t pubkey[897])
{
  int16_t h[512];
  polymat_t pk;
  poly_t p;

  if (pubkey == NULL || falcon_decode_pubkey_chk (h, pubkey) != 0)
    return 0;

  poly_alloc (p, cbdc_falcon_ring);
  poly_set_coeffvec_i16 (p, h);
  polymat_alloc (pk, cbdc_falcon_ring, 1, 1);
  polymat_set_elem (pk, 0, 0, p);

  polymat_alloc (B2, cbdc_ring, 8, 8);
  lin_toisoring (B2, NULL, pk, NULL);
  polymat_redc (B2, B2);

  polymat_free (pk);
  poly_free (p);
  return 1;
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
 * Decode a credential tau,s1,s2 from the credlen bytes at cred. The decoder
 * never reads past them. Returns 1 if it decodes and consumes exactly credlen
 * bytes.
 */
static int
cbdc_dec_cred (polyvec_t tau, polyvec_t s1, polyvec_t s2, const uint8_t *cred,
               size_t credlen)
{
  coder_state_t cstate;
  uint8_t tau_[64];
  unsigned int off;
  int rv = 0;

  if (cred == NULL || credlen == 0 || credlen > cbdc_maxlen_cred ())
    return 0;
  coder_dec_begin_bnd (cstate, cred, credlen);
  if (coder_dec_bytes (cstate, tau_, sizeof (tau_)) != 0)
    goto out;
  coder_dec_grandom3 (cstate, s1, CBDC_LOG2O_BLINDSIG);
  coder_dec_grandom3 (cstate, s2, CBDC_LOG2O_BLINDSIG);
  if (coder_get_err (cstate) || coder_dec_end (cstate) != 1)
    goto out;
  off = coder_get_offset (cstate);
  if (off % 8 != 0 || (size_t)(off >> 3) != credlen)
    goto out;
  cbdc_expand_tau (tau, tau_);
  rv = 1;
out:
  cbdc_wipe (tau_, sizeof (tau_));
  return rv;
}

void
cbdc_issuer_keygen (uint8_t sk[1281], uint8_t pk[897])
{
  falcon_keygen (sk, pk);
}

/* user */

/*
 * Returns 1 on success, 0 if pubkey is not a valid Falcon-512 public key, in
 * which case state is zeroed and need not be cleared.
 */
int
cbdc_user_init (cbdc_user_state_t state, const uint8_t pubkey[897])
{
  cbdc_pp_init ();

  memset (state, 0, sizeof (cbdc_user_state_t));
  if (!cbdc_decode_pk (state->B2, pubkey))
    return 0;
  cbdc_derive_c0 (state->c0, pubkey);
  state->ready = 1;
  return 1;
}

/* forget the masking randomness and the attributes */
static void
cbdc_user_forget (cbdc_user_state_t state)
{
  if (state->masked)
    {
      cbdc_vec_free_secret (state->r);
      cbdc_vec_free_secret (state->m);
      state->masked = 0;
    }
}

/* Release the state; it may be called more than once. */
void
cbdc_user_clear (cbdc_user_state_t state)
{
  if (!state->ready)
    return;
  if (state->p1_ready)
    lin_prover_clear (state->p1);
  polymat_free (state->B2);
  polyvec_free (state->c0);
  cbdc_user_forget (state);
  cbdc_wipe (state, sizeof (cbdc_user_state_t));
}

/*
 * Encode a polyvec with coefficients in Z_12289 into at most cap bytes at
 * out. Returns the length, 0 if it does not fit.
 */
static size_t
cbdc_enc_vec (uint8_t *out, size_t cap, polyvec_t v)
{
  coder_state_t cstate;
  unsigned int len;
  INT_T (mod, 1);

  if (out == NULL)
    return 0;
  int_set_i64 (mod, CBDC_MOD);
  polyvec_redp (v, v);

  coder_enc_begin_bnd (cstate, out, cap);
  coder_enc_urandom3 (cstate, v, mod, CBDC_LOGMOD);
  coder_enc_end (cstate);
  if (coder_get_err (cstate))
    return 0;
  len = coder_get_offset (cstate);
  if (len % 8 != 0 || (size_t)(len >> 3) > cap)
    return 0;
  return len >> 3;
}

/*
 * Decode a polyvec from the first inlen bytes of in, returns the number of
 * bytes read or -1. The decoder never reads past in + inlen.
 */
static long
cbdc_dec_vec (polyvec_t v, const uint8_t *in, size_t inlen)
{
  coder_state_t cstate;
  unsigned int off;
  INT_T (mod, 1);

  if (in == NULL || inlen == 0)
    return -1;

  int_set_i64 (mod, CBDC_MOD);

  coder_dec_begin_bnd (cstate, in, inlen);
  if (coder_dec_urandom3 (cstate, v, mod, CBDC_LOGMOD) != 0)
    return -1;
  if (coder_get_err (cstate) || coder_dec_end (cstate) != 1)
    return -1;
  off = coder_get_offset (cstate);
  if (off % 8 != 0 || (size_t)(off >> 3) > inlen)
    return -1;
  polyvec_redc (v, v);
  return off >> 3;
}

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

  cbdc_vec_free_secret (s);
  polymat_free (Acom);
}

/* encode nym to exactly CBDC_NYMLEN bytes. Returns 1 on success. */
static int
cbdc_nym_encode (uint8_t *nym, size_t *nymlen, polyvec_t nymv)
{
  /* encoding reduces to [0,q) in place, so encode a copy */
  polyvec_t tmp;
  size_t len;

  polyvec_alloc (tmp, cbdc_ring, CBDC_COM_ROWS);
  polyvec_set (tmp, nymv);
  len = cbdc_enc_vec (nym, CBDC_NYMLEN, tmp);
  polyvec_free (tmp);
  *nymlen = len;
  return len == CBDC_NYMLEN;
}

/*
 * nym = Acom*(attr,r) with internal coins for r. Returns 1 on success, in
 * which case com must be cleared by the caller.
 */
int
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

  memset (com, 0, sizeof (cbdc_commit_t));
  if (attr == NULL || nym == NULL || nymlen == NULL)
    return 0;

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
  cbdc_wipe (seed, sizeof (seed));

  cbdc_commit_compute (com, a);
  cbdc_vec_free_secret (a);
  if (!cbdc_nym_encode (nym, nymlen, com->nym))
    {
      cbdc_commit_clear (com);
      return 0;
    }
  return 1;
}

/* Release a commitment, wiping its opening. */
void
cbdc_commit_clear (cbdc_commit_t com)
{
  polyvec_free (com->nym);
  cbdc_vec_free_secret (com->r);
  cbdc_wipe (com, sizeof (cbdc_commit_t));
}

/*
 * Encode the opening (the randomness r) of a commitment into
 * cbdc_maxlen_opening() bytes. Returns 1 on success.
 */
int
cbdc_commit_export (cbdc_commit_t com, uint8_t *opening, size_t *openinglen)
{
  polyvec_t tmp;
  size_t len;

  if (opening == NULL || openinglen == NULL)
    return 0;
  polyvec_alloc (tmp, cbdc_ring, 10);
  polyvec_set (tmp, com->r);
  len = cbdc_enc_vec (opening, CBDC_OPENINGLEN, tmp);
  cbdc_vec_free_secret (tmp);
  *openinglen = len;
  return len == CBDC_OPENINGLEN;
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

  memset (com, 0, sizeof (cbdc_commit_t));
  if (attr == NULL || nym == NULL || nymlen == NULL)
    return 0;
  polyvec_alloc (com->r, cbdc_ring, 10);
  if (!cbdc_opening_decode (com->r, opening, openinglen))
    {
      cbdc_vec_free_secret (com->r);
      memset (com, 0, sizeof (cbdc_commit_t));
      return 0;
    }
  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  cbdc_commit_compute (com, a);
  cbdc_vec_free_secret (a);
  if (!cbdc_nym_encode (nym, nymlen, com->nym))
    {
      cbdc_commit_clear (com);
      return 0;
    }
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

  if (nymlen != CBDC_NYMLEN || attr == NULL)
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
      cbdc_vec_free_secret (a);
    }
  cbdc_vec_free_secret (com->r);
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

/* com holds a commitment (cbdc_user_commit or cbdc_commit_import) */
static int
cbdc_commit_ready (cbdc_commit_srcptr com)
{
  return com != NULL && com->r->nelems == 10 && com->r->elems != NULL
         && com->nym->nelems == CBDC_COM_ROWS && com->nym->elems != NULL;
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
  cbdc_vec_free_secret (a);
  return b;
}

/*
 * Signature of knowledge of the opening of com (attr,r), bound to the
 * context ctx (e.g. a domain separator and the message). proof must hold
 * cbdc_maxlen_sok() bytes. Returns 1 on success, 0 if com is not a
 * commitment to attr (the prover is never run on a false statement) or the
 * proof does not fit.
 */
static int
cbdc_sokprove (uint8_t *proof, size_t *proofl, cbdc_commit_t com,
               const uint8_t *ctx, size_t ctxlen, const uint8_t attr[32],
               const uint8_t *coins, int shared)
{
  lin_prover_state_t prover;
  polyvec_t a, s, t;
  polymat_t Acom;
  int rv;

  cbdc_pp_init ();

  if (proof == NULL || proofl == NULL || attr == NULL
      || !cbdc_commit_ready (com))
    return 0;
  *proofl = 0;

  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  if (!cbdc_com_ok (com, polyvec_get_elem (a, 0)))
    {
      cbdc_vec_free_secret (a);
      return 0;
    }

  cbdc_mat_copy (Acom, cbdc_Acom);
  polyvec_alloc (s, cbdc_ring, 1 + 10);
  cbdc_put (s, 0, a);
  cbdc_put (s, 1, com->r);
  cbdc_sok_statement (t, com->nym);

  if (shared)
    lin_prover_init_pp (prover, cbdc_popenlpp);
  else
    /* private matrices: only used by the shared == 0 test path */
    lin_prover_init (prover, cbdc_popenpp, popen_param);
  lin_prover_set_statement (prover, Acom, t);
  lin_prover_set_witness (prover, s);
  rv = cbdc_prove_coins (prover, proof, cbdc_maxlen_sok (), proofl, ctx,
                         ctxlen, coins);
  lin_prover_clear (prover);

  cbdc_vec_free_secret (a);
  cbdc_vec_free_secret (s);
  polyvec_free (t);
  polymat_free (Acom);
  return rv == 1;
}

int
cbdc_user_sokprove (uint8_t *proof, size_t *proofl, cbdc_commit_t com,
                    const uint8_t *ctx, size_t ctxlen, const uint8_t attr[32])
{
  return cbdc_sokprove (proof, proofl, com, ctx, ctxlen, attr, NULL, 1);
}

/*
 * Statement of the equality-of-opening proof: [0 | Acom_r]*(x,r) + t = 0 with
 * t = Acom_0*a - nym, that is, nym - Acom_0*a = Acom_r*r for a short r. The
 * column of the attribute is zeroed, so the proof shows that nym commits to
 * the public attribute a itself (a proof of knowledge of an opening of nym
 * would also accept a + x for any short x).
 */
static void
cbdc_eqopen_statement (polymat_t A, polyvec_t t, polyvec_t nym, polyvec_t a)
{
  unsigned int i;
  polyvec_t a0;

  cbdc_mat_copy (A, cbdc_Acom);
  polyvec_alloc (t, cbdc_ring, CBDC_COM_ROWS);
  polyvec_alloc (a0, cbdc_ring, 11);
  polyvec_set_zero (a0);
  cbdc_put (a0, 0, a);
  polyvec_mul (t, A, a0);
  polyvec_sub (t, t, nym, 0);
  polyvec_redc (t, t);
  polyvec_free (a0);
  for (i = 0; i < CBDC_COM_ROWS; i++)
    poly_set_zero (polymat_get_elem (A, i, 0));
}

/*
 * Proof that com is a commitment to the public attribute attr, bound to ctx,
 * revealing nothing about the opening. proof must hold cbdc_maxlen_sok()
 * bytes. Returns 1 on success, 0 if com is not a commitment to attr (the
 * prover is never run on a false statement) or the proof does not fit.
 */
int
cbdc_user_eqopenprove (uint8_t *proof, size_t *proofl, cbdc_commit_t com,
                       const uint8_t *ctx, size_t ctxlen,
                       const uint8_t attr[32])
{
  lin_prover_state_t prover;
  polyvec_t a, s, t;
  polymat_t A;
  int rv;

  cbdc_pp_init ();

  if (proof == NULL || proofl == NULL || attr == NULL
      || !cbdc_commit_ready (com))
    return 0;
  *proofl = 0;

  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  if (!cbdc_com_ok (com, polyvec_get_elem (a, 0)))
    {
      cbdc_vec_free_secret (a);
      return 0;
    }

  cbdc_eqopen_statement (A, t, com->nym, a);
  polyvec_alloc (s, cbdc_ring, 1 + 10);
  polyvec_set_zero (s);
  cbdc_put (s, 1, com->r);

  lin_prover_init_pp (prover, cbdc_popenlpp);
  lin_prover_set_statement (prover, A, t);
  lin_prover_set_witness (prover, s);
  rv = cbdc_prove (prover, proof, cbdc_maxlen_sok (), proofl, ctx, ctxlen);
  lin_prover_clear (prover);

  cbdc_vec_free_secret (a);
  cbdc_vec_free_secret (s);
  polyvec_free (t);
  polymat_free (A);
  return rv == 1;
}

/*
 * attrs: 5*32 bytes, masked_attrs: cbdc_maxlen_masked_attrs() bytes. Returns
 * 1 on success, 0 if the state is not initialized, idx_rev is invalid, the
 * attributes do not satisfy the norm bound of P1 (cbdc_attrs_ok) or the
 * output does not fit. On failure a previous masking is kept.
 */
int
cbdc_user_maskattrs (cbdc_user_state_t state, uint8_t *masked_attrs,
                     size_t *masked_attrslen, const uint8_t *attrs,
                     const unsigned int *idx_rev, unsigned int nrev)
{
  polyvec_t m, r, t, tmp, u, w, mpub, mpriv;
  polymat_t A;
  uint8_t seed[32];
  uint8_t dom = 0;
  size_t tlen, plen = 0;
  const size_t cap = cbdc_maxlen_masked_attrs ();
  int rc;
  INT_T (l2sqr, 2);
  INT_T (bnd, 2);

  cbdc_pp_init ();

  if (!state->ready || masked_attrs == NULL
      || masked_attrslen == NULL || attrs == NULL || (nrev > 0 && idx_rev == NULL)
      || !cbdc_idx_valid (idx_rev, nrev) || !cbdc_attrs_ok (attrs))
    return 0;
  *masked_attrslen = 0;

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
  cbdc_wipe (seed, sizeof (seed));

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

  /* the issuance prover only lives for this call */
  lin_prover_init_pp (state->p1, cbdc_p1lpp);
  state->p1_ready = 1;
  lin_prover_set_statement (state->p1, A, u);
  lin_prover_set_witness (state->p1, w);

  /* masked attrs = enc(t) || P1 */
  tlen = cbdc_enc_vec (masked_attrs, cap, t);
  rc = tlen > 0
       && cbdc_prove (state->p1, masked_attrs + tlen, cap - tlen, &plen, NULL,
                      0);
  lin_prover_clear (state->p1);
  state->p1_ready = 0;

  if (rc)
    {
      /* save randomness and message */
      cbdc_user_forget (state);
      state->r[0] = r[0];
      state->m[0] = m[0];
      state->masked = 1;
      *masked_attrslen = tlen + plen;
    }
  else
    {
      cbdc_vec_free_secret (r);
      cbdc_vec_free_secret (m);
    }

  polyvec_free (t);
  cbdc_vec_free_secret (tmp);
  polyvec_free (u);
  cbdc_vec_free_secret (w);
  cbdc_vec_free_secret (mpub);
  cbdc_vec_free_secret (mpriv);
  polymat_free (A);
  return rc;
}

/*
 * Ar*r + Atau*tau + Am*m + c0 = s1 + B2*s2 and l2(s1,s2) is bounded, for the
 * r and m in state. Returns 1 if the credential is valid.
 */
static int
cbdc_cred_ok (cbdc_user_state_t state, polyvec_t tau, polyvec_t s1,
              polyvec_t s2)
{
  polyvec_t acc, tmp, zero, r, m, c0;
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
  /* the state is shared by concurrent presentations and polyvec_mul converts
     its operands in place: work on copies */
  polyvec_alloc (r, cbdc_ring, 16);
  polyvec_set (r, state->r);
  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  polyvec_set (m, state->m);
  polyvec_alloc (c0, cbdc_ring, 8);
  polyvec_set (c0, state->c0);

  polyvec_alloc (acc, cbdc_ring, 8);
  polyvec_alloc (tmp, cbdc_ring, 8);
  polyvec_alloc (zero, cbdc_ring, 8);
  polyvec_set_zero (zero);

  polyvec_mul (acc, AR, r);
  polyvec_mul (tmp, ATAU, tau);
  polyvec_add (acc, acc, tmp, 0);
  polyvec_mul (tmp, AM, m);
  polyvec_add (acc, acc, tmp, 0);
  polyvec_add (acc, acc, c0, 0);
  polyvec_sub (acc, acc, s1, 0);
  polyvec_mul (tmp, B2, s2);
  polyvec_sub (acc, acc, tmp, 0);
  polyvec_redc (acc, acc);
  rv = polyvec_eq (acc, zero) == 1;

  cbdc_vec_free_secret (acc);
  cbdc_vec_free_secret (tmp);
  cbdc_vec_free_secret (r);
  cbdc_vec_free_secret (m);
  polyvec_free (c0);
  polyvec_free (zero);
  polymat_free (AR);
  polymat_free (ATAU);
  polymat_free (AM);
  polymat_free (B2);
  return rv;
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
 * The presentation of cbdc_user_presgen. The state is only read, so
 * presentations of one state may run concurrently: the statement and the
 * witness are built from copies, and the P2 prover is set up for this call
 * from the shared public matrices (or, for tests, with private ones,
 * shared == 0) and cleared before returning, which wipes the witness.
 */
static int
cbdc_presgen (cbdc_user_state_t state, uint8_t *pres, size_t *preslen,
              const uint8_t *cred, size_t credlen,
              const unsigned int *idx_rev, unsigned int nrev,
              cbdc_commit_ptr *coms, const unsigned int *idx_com,
              unsigned int ncom, const uint8_t *ctx, size_t ctxlen,
              const uint8_t *coins, int shared)
{
  lin_prover_state_t prover;
  polyvec_t tau, s1, s2, w, u, m, c0;
  polyvec_t nyms[CBDC_MAXCOM];
  polymat_t A, B2;
  unsigned int i;
  int rv = 0;

  cbdc_pp_init ();

  if (!state->ready || !state->masked || pres == NULL || preslen == NULL
      || (nrev > 0 && idx_rev == NULL) || (ncom > 0 && (idx_com == NULL || coms == NULL))
      || !cbdc_com_valid (idx_rev, nrev, idx_com, ncom))
    return 0;
  *preslen = 0;

  polyvec_alloc (tau, cbdc_ring, 8);
  polyvec_alloc (s1, cbdc_ring, 8);
  polyvec_alloc (s2, cbdc_ring, 8);
  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  polyvec_set (m, state->m);

  /*
   * decode blindsig tau,s1,s2 and check the whole witness: the prover must
   * not be run on a false statement (it does not terminate cleanly then).
   */
  if (!cbdc_dec_cred (tau, s1, s2, cred, credlen)
      || !cbdc_cred_ok (state, tau, s1, s2))
    goto err_dec;
  for (i = 0; i < ncom; i++)
    if (!cbdc_commit_ready (coms[i])
        || !cbdc_com_ok (coms[i], polyvec_get_elem (m, idx_com[i])))
      goto err_dec;

  for (i = 0; i < ncom; i++)
    nyms[i][0] = coms[i]->nym[0];
  /* B2 and c0 are in the coefficient domain, which poly_neg and polyvec_add
     leave alone, but copy them as well: the state is shared */
  cbdc_mat_copy (B2, state->B2);
  polyvec_alloc (c0, cbdc_ring, 8);
  polyvec_set (c0, state->c0);
  cbdc_p2_statement (A, u, B2, c0, m, idx_rev, nrev, nyms, idx_com, ncom);
  polyvec_free (c0);
  polymat_free (B2);

  /* w = (r,tau,s1,s2,mpriv,rcom) */
  {
    polyvec_t mpub, mpriv;

    cbdc_split (mpub, mpriv, m, idx_rev, nrev);
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
    cbdc_vec_free_secret (mpub);
    cbdc_vec_free_secret (mpriv);
  }

  if (shared)
    lin_prover_init_pp (prover, cbdc_p2lpp);
  else
    /* private matrices: only used by the shared == 0 test path */
    lin_prover_init (prover, cbdc_p2pp, p2_param);
  lin_prover_set_statement (prover, A, u);
  lin_prover_set_witness (prover, w);
  rv = cbdc_prove_coins (prover, pres, cbdc_maxlen_pres (), preslen, ctx,
                         ctxlen, coins);
  /* also wipes the prover's copy of the witness */
  lin_prover_clear (prover);

  cbdc_vec_free_secret (w);
  polyvec_free (u);
  polymat_free (A);
err_dec:
  cbdc_vec_free_secret (tau);
  cbdc_vec_free_secret (s1);
  cbdc_vec_free_secret (s2);
  cbdc_vec_free_secret (m);
  return rv;
}

/*
 * coms[i] is the commitment to attribute idx_com[i]. The presentation is
 * bound to ctx. Returns 1 on success. The state is only read: presentations
 * of one state may run concurrently, also with cbdc_user_credverify, but not
 * with what writes it (maskattrs, import, clear).
 */
int
cbdc_user_presgen (cbdc_user_state_t state, uint8_t *pres, size_t *preslen,
                   const uint8_t *cred, size_t credlen,
                   const unsigned int *idx_rev, unsigned int nrev,
                   cbdc_commit_ptr *coms, const unsigned int *idx_com,
                   unsigned int ncom, const uint8_t *ctx, size_t ctxlen)
{
  return cbdc_presgen (state, pres, preslen, cred, credlen, idx_rev, nrev,
                       coms, idx_com, ncom, ctx, ctxlen, NULL, 1);
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
  int rv = 0;

  cbdc_pp_init ();

  if (!state->ready || !state->masked)
    return 0;

  polyvec_alloc (tau, cbdc_ring, 8);
  polyvec_alloc (s1, cbdc_ring, 8);
  polyvec_alloc (s2, cbdc_ring, 8);

  if (!cbdc_dec_cred (tau, s1, s2, cred, credlen))
    goto err_dec;

  rv = cbdc_cred_ok (state, tau, s1, s2);

err_dec:
  cbdc_vec_free_secret (tau);
  cbdc_vec_free_secret (s1);
  cbdc_vec_free_secret (s2);
  return rv;
}

/*
 * User state import/export: only the masking randomness r is secret. out
 * must hold cbdc_maxlen_userstate() bytes. Returns 1 on success, 0 if the
 * state is not initialized or holds no masking (neither cbdc_user_maskattrs
 * nor cbdc_user_import has succeeded).
 */
int
cbdc_user_export (cbdc_user_state_t state, uint8_t *out, size_t *outlen)
{
  polyvec_t tmp;
  size_t len;

  if (!state->ready || !state->masked || out == NULL || outlen == NULL)
    return 0;
  polyvec_alloc (tmp, cbdc_ring, 16);
  polyvec_set (tmp, state->r);
  len = cbdc_enc_vec (out, CBDC_USERSTATELEN, tmp);
  cbdc_vec_free_secret (tmp);
  *outlen = len;
  return len == CBDC_USERSTATELEN;
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

  if (!state->ready || inlen != CBDC_USERSTATELEN || attrs == NULL)
    return 0;
  polyvec_alloc (r, cbdc_ring, 16);
  if (cbdc_dec_vec (r, in, inlen) != (long)CBDC_USERSTATELEN)
    {
      cbdc_vec_free_secret (r);
      return 0;
    }
  int_set_i64 (bnd, CBDC_L2SQR_BND_R);
  polyvec_l2sqr (l2sqr, r);
  if (int_gt (l2sqr, bnd))
    {
      cbdc_vec_free_secret (r);
      return 0;
    }
  cbdc_user_forget (state);
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

  if (attrs == NULL)
    return 0;
  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs, CBDC_NATTRS);
  int_set_i64 (bnd, CBDC_L2SQR_BND_ATTRS);
  polyvec_l2sqr (l2sqr, m);
  b = !int_gt (l2sqr, bnd);
  cbdc_vec_free_secret (m);
  return b;
}

/* issuer */

/*
 * Returns 1 on success, 0 if privkey is not a well-formed Falcon-512 private
 * key whose public key is pubkey, in which case state is zeroed and need not
 * be cleared.
 */
int
cbdc_issuer_init (cbdc_issuer_state_t state, const uint8_t pubkey[897],
                  const uint8_t privkey[1281])
{
  cbdc_pp_init ();

  memset (state, 0, sizeof (cbdc_issuer_state_t));
  if (pubkey == NULL || privkey == NULL
      || falcon_check_keypair (privkey, pubkey) != 0)
    return 0;
  memcpy (state->sk, privkey, sizeof (state->sk));
  cbdc_derive_c0 (state->c0, pubkey);
  lin_verifier_init_pp (state->p1, cbdc_p1lpp);
  state->ready = 1;
  return 1;
}

/* Release the state and wipe the private key; it may be called more than once. */
void
cbdc_issuer_clear (cbdc_issuer_state_t state)
{
  if (!state->ready)
    {
      cbdc_wipe (state->sk, sizeof (state->sk));
      return;
    }
  lin_verifier_clear (state->p1);
  polyvec_free (state->c0);
  cbdc_wipe (state, sizeof (cbdc_issuer_state_t));
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

  if (!state->ready || cred == NULL || credlen == NULL || attrs_rev == NULL
      || (nrev > 0 && idx_rev == NULL) || !cbdc_idx_valid (idx_rev, nrev)
      || masked_attrs == NULL || masked_attrslen == 0
      || masked_attrslen > cbdc_maxlen_masked_attrs ())
    return 0;
  *credlen = 0;

  polyvec_alloc (m, cbdc_ring, CBDC_NATTRS);
  cbdc_encattrs (m, attrs_rev, CBDC_NATTRS);

  /* decode masked attributes, never reading past masked_attrslen bytes */
  polyvec_alloc (t, cbdc_ring, 8);
  tlen = cbdc_dec_vec (t, masked_attrs,
                       masked_attrslen < 1024 ? masked_attrslen : 1024);
  if (tlen < 0)
    goto err_t;
  off = (size_t)tlen;

  /* verify proof for PoK(w): Aw + u = 0 */
  cbdc_p1_statement (A, u, t, idx_rev, nrev, m);
  lin_verifier_set_statement (state->p1, A, u);
  b = lin_verifier_verify_ctx_bnd (state->p1, masked_attrs + off,
                                   masked_attrslen - off, &len, NULL, 0);
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
  if (falcon_preimage_sample_chk (s1, s2, tf, state->sk) != 0)
    {
      b = 0;
      poly_free (fp);
      polyvec_free (tau);
      polyvec_free (tmp);
      polyvec_free (tp);
      goto err_t;
    }

  polyvec_alloc (s1v, cbdc_ring, 8);
  polyvec_alloc (s2v, cbdc_ring, 8);
  poly_set_coeffvec_i16 (fp, s1);
  poly_toisoring (s1v, fp);
  poly_set_coeffvec_i16 (fp, s2);
  poly_toisoring (s2v, fp);

  /* encode blindsig tau,s1,s2 into at most cbdc_maxlen_cred() bytes */
  coder_enc_begin_bnd (cstate, cred, cbdc_maxlen_cred ());
  coder_enc_bytes (cstate, tau_, sizeof (tau_));
  coder_enc_grandom3 (cstate, s1v, CBDC_LOG2O_BLINDSIG);
  coder_enc_grandom3 (cstate, s2v, CBDC_LOG2O_BLINDSIG);
  coder_enc_end (cstate);
  len = coder_get_offset (cstate);
  b = !coder_get_err (cstate) && len % 8 == 0
      && (len >> 3) <= cbdc_maxlen_cred ();
  *credlen = b ? len >> 3 : 0;

  cbdc_wipe (s1, sizeof (s1));
  cbdc_wipe (s2, sizeof (s2));
  cbdc_wipe (tau_, sizeof (tau_));
  poly_wipe (fp);
  poly_free (fp);
  cbdc_vec_free_secret (tau);
  polyvec_free (tmp);
  polyvec_free (tp);
  cbdc_vec_free_secret (s1v);
  cbdc_vec_free_secret (s2v);
err_t:
  polyvec_free (t);
  polyvec_free (m);
  return b;
}

/* verifier */

/*
 * Returns 1 on success, 0 if pubkey is not a valid Falcon-512 public key, in
 * which case state is zeroed and need not be cleared.
 */
int
cbdc_verifier_init (cbdc_verifier_state_t state, const uint8_t pubkey[897])
{
  cbdc_pp_init ();

  memset (state, 0, sizeof (cbdc_verifier_state_t));
  if (!cbdc_decode_pk (state->B2, pubkey))
    return 0;
  cbdc_derive_c0 (state->c0, pubkey);
  lin_verifier_init_pp (state->p2, cbdc_p2lpp);
  state->ready = 1;
  return 1;
}

/* Release the state; it may be called more than once. */
void
cbdc_verifier_clear (cbdc_verifier_state_t state)
{
  if (!state->ready)
    return;
  lin_verifier_clear (state->p2);
  polymat_free (state->B2);
  polyvec_free (state->c0);
  memset (state, 0, sizeof (cbdc_verifier_state_t));
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
  size_t len = 0;
  unsigned int i, nok = 0;
  int b = 0;

  cbdc_pp_init ();

  if (!state->ready || attrs_rev == NULL || (nrev > 0 && idx_rev == NULL)
      || (ncom > 0 && (idx_com == NULL || nyms == NULL || nymlens == NULL))
      || !cbdc_com_valid (idx_rev, nrev, idx_com, ncom) || pres == NULL
      || preslen == 0 || preslen > cbdc_maxlen_pres ())
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
  b = lin_verifier_verify_ctx_bnd (state->p2, pres, preslen, &len, ctx,
                                   ctxlen);
  if (b == 1 && len != preslen)
    b = 0;

  polyvec_free (u);
  polymat_free (A);
  polyvec_free (m);
out:
  for (i = 0; i < nok; i++)
    polyvec_free (nymv[i]);
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
  size_t len = 0;
  int b = 0;

  cbdc_pp_init ();

  if (nymlen != CBDC_NYMLEN || proof == NULL || proofl == 0
      || proofl > cbdc_maxlen_sok ())
    return 0;

  polyvec_alloc (nymv, cbdc_ring, CBDC_COM_ROWS);
  if (cbdc_dec_vec (nymv, nym, nymlen) != (long)CBDC_NYMLEN)
    {
      polyvec_free (nymv);
      return 0;
    }
  cbdc_sok_statement (t, nymv);
  cbdc_mat_copy (Acom, cbdc_Acom);

  lin_verifier_init_pp (verifier, cbdc_popenlpp);
  lin_verifier_set_statement (verifier, Acom, t);
  b = lin_verifier_verify_ctx_bnd (verifier, proof, proofl, &len, ctx, ctxlen);
  if (b == 1 && len != proofl)
    b = 0;
  lin_verifier_clear (verifier);

  polymat_free (Acom);
  polyvec_free (t);
  polyvec_free (nymv);
  return b == 1;
}

/*
 * Returns 1 if proof (cbdc_user_eqopenprove) proves, bound to ctx, that nym
 * commits to the public attribute attr. The proof must be consumed exactly.
 */
int
cbdc_verifier_eqopenverify (const uint8_t *nym, size_t nymlen,
                            const uint8_t attr[32], const uint8_t *ctx,
                            size_t ctxlen, const uint8_t *proof,
                            size_t proofl)
{
  lin_verifier_state_t verifier;
  polyvec_t nymv, a, t;
  polymat_t A;
  size_t len = 0;
  int b = 0;

  cbdc_pp_init ();

  if (nymlen != CBDC_NYMLEN || attr == NULL || proof == NULL || proofl == 0
      || proofl > cbdc_maxlen_sok ())
    return 0;

  polyvec_alloc (nymv, cbdc_ring, CBDC_COM_ROWS);
  if (cbdc_dec_vec (nymv, nym, nymlen) != (long)CBDC_NYMLEN)
    {
      polyvec_free (nymv);
      return 0;
    }
  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  cbdc_eqopen_statement (A, t, nymv, a);

  lin_verifier_init_pp (verifier, cbdc_popenlpp);
  lin_verifier_set_statement (verifier, A, t);
  b = lin_verifier_verify_ctx_bnd (verifier, proof, proofl, &len, ctx, ctxlen);
  if (b == 1 && len != proofl)
    b = 0;
  lin_verifier_clear (verifier);

  polymat_free (A);
  polyvec_free (t);
  polyvec_free (a);
  polyvec_free (nymv);
  return b == 1;
}

/*
 * Bit offset of the hints in an encoded proof of the proof system which (1:
 * P1, 2: P2, 3: SoK), 0 for another value. The hints follow the fixed-width
 * part of the proof (tB, h, tA1, c) and start the variable-length part. Only
 * used to craft malformed proofs in tests.
 */
size_t
cbdc_proof_hint_offset (int which)
{
  const lin_params_struct *params;
  lnp_tbox_params_srcptr P;
  abdlop_params_srcptr T;
  size_t log2q, D;

  switch (which)
    {
    case 1:
      params = p1_param;
      break;
    case 2:
      params = p2_param;
      break;
    case 3:
      params = popen_param;
      break;
    default:
      return 0;
    }
  P = params->tbox_params;
  T = P->tbox;
  log2q = T->ring->log2q;
  D = P->quad_eval->quad_many->dcompress->D;
  return (size_t)(T->l + T->lext) * T->ring->d * log2q
         + (size_t)(P->quad_eval->lambda / 2) * T->ring->d * log2q
         + (size_t)T->kmsis * T->ring->d * (log2q - D)
         + (size_t)T->ring->d * T->log2omega;
}

/*
 * For tests only: n samples of the discrete gaussian of lazer with sigma
 * 1.55*2^log2o, as the provers draw their masking vectors. Returns 1 on
 * success.
 */
int
cbdc_test_grandom (int64_t *out, unsigned int n, unsigned int log2o,
                   const uint8_t seed[32], uint32_t dom)
{
  intvec_t v;
  unsigned int i;

  /* intvec_grandom samples into a variable length array on the stack */
  if (out == NULL || seed == NULL || n == 0 || n > 65536 || log2o == 0
      || log2o > 40)
    return 0;
  intvec_alloc (v, n, 1);
  intvec_grandom (v, log2o, seed, dom);
  for (i = 0; i < n; i++)
    out[i] = int_get_i64 (intvec_get_elem (v, i));
  intvec_free (v);
  return 1;
}

/*
 * For tests only: cbdc_user_presgen with the given prover coins, with the
 * shared public matrices (shared != 0) or private ones. Honest proofs: the
 * same coins must give the same bytes either way.
 */
int
cbdc_test_presgen_coins (cbdc_user_state_t state, uint8_t *pres,
                         size_t *preslen, const uint8_t *cred, size_t credlen,
                         cbdc_commit_ptr *coms, const unsigned int *idx_com,
                         unsigned int ncom, const uint8_t *ctx, size_t ctxlen,
                         const uint8_t coins[32], int shared)
{
  if (coins == NULL)
    return 0;
  return cbdc_presgen (state, pres, preslen, cred, credlen, NULL, 0, coms,
                       idx_com, ncom, ctx, ctxlen, coins, shared);
}

/* For tests only: cbdc_user_sokprove with the given coins, see above. */
int
cbdc_test_sokprove_coins (uint8_t *proof, size_t *proofl, cbdc_commit_t com,
                          const uint8_t *ctx, size_t ctxlen,
                          const uint8_t attr[32], const uint8_t coins[32],
                          int shared)
{
  if (coins == NULL)
    return 0;
  return cbdc_sokprove (proof, proofl, com, ctx, ctxlen, attr, coins, shared);
}

/* absorb the raw representation of a matrix into h */
static void
cbdc_absorb_mat (shake128_state_t h, polymat_srcptr M)
{
  unsigned int i, j;

  for (i = 0; i < M->nrows; i++)
    for (j = 0; j < M->ncols; j++)
      {
        /* read the struct directly: poly_get_coeff and poly_get_coeffvec
           convert from the CRT domain, which would write the matrix */
        poly_srcptr p = M->elems + i * M->stride_row + j * M->stride_col;
        intvec_srcptr c = p->coeffs;
        uint8_t flag = (uint8_t)p->crt;

        shake128_absorb (h, &flag, 1);
        shake128_absorb (h, (const uint8_t *)c->bytes,
                         c->nelems * _sizeof_int_data (c->nlimbs));
        if (p->crt && p->crtrep != NULL)
          shake128_absorb (h, (const uint8_t *)p->crtrep,
                           sizeof (crtcoeff_t) * p->ring->d
                               * p->ring->nmoduli);
      }
}

/*
 * For tests only: a digest of the shared public matrices (all proof systems)
 * and of the scheme's matrices, from their raw representation, to check that
 * nothing writes them.
 */
void
cbdc_test_pp_digest (uint8_t out[32])
{
  shake128_state_t h;
  const lin_pp_struct *pps[3];
  unsigned int k;

  cbdc_pp_init ();
  pps[0] = cbdc_p1lpp;
  pps[1] = cbdc_p2lpp;
  pps[2] = cbdc_popenlpp;
  shake128_init (h);
  for (k = 0; k < 3; k++)
    {
      cbdc_absorb_mat (h, pps[k]->A1);
      cbdc_absorb_mat (h, pps[k]->A2prime);
      cbdc_absorb_mat (h, pps[k]->Bprime);
    }
  cbdc_absorb_mat (h, cbdc_AR);
  cbdc_absorb_mat (h, cbdc_AM);
  cbdc_absorb_mat (h, cbdc_ATAU);
  cbdc_absorb_mat (h, cbdc_Acom);
  shake128_squeeze (h, out, 32);
  shake128_clear (h);
}

/*
 * For tests only: 1 if the statement of the equality-of-opening proof for
 * the nym of com and the attribute attr has a zero attribute column, the
 * other columns of Acom, and t = Acom_0*enc(attr) - nym; 0 otherwise.
 */
int
cbdc_test_eqopen_statement_ok (cbdc_commit_t com, const uint8_t attr[32])
{
  polymat_t A, Acom;
  polyvec_t a, a0, t, want;
  unsigned int i, j;
  int ok = 1;

  cbdc_pp_init ();

  if (attr == NULL || !cbdc_commit_ready (com))
    return 0;
  polyvec_alloc (a, cbdc_ring, 1);
  cbdc_encattrs (a, attr, 1);
  cbdc_eqopen_statement (A, t, com->nym, a);
  /* redc and polyvec_mul below write their operands: work on a copy of the
     shared commitment key */
  cbdc_mat_copy (Acom, cbdc_Acom);

  for (i = 0; i < CBDC_COM_ROWS; i++)
    for (j = 0; j < 11; j++)
      {
        poly_ptr e = polymat_get_elem (A, i, j);
        poly_ptr f = polymat_get_elem (Acom, i, j);
        poly_t zero;

        poly_alloc (zero, cbdc_ring);
        poly_set_zero (zero);
        poly_redc (e, e);
        poly_redc (f, f);
        if (poly_eq (e, j == 0 ? zero : f) != 1)
          ok = 0;
        poly_free (zero);
      }

  polyvec_alloc (a0, cbdc_ring, 11);
  polyvec_set_zero (a0);
  cbdc_put (a0, 0, a);
  polyvec_alloc (want, cbdc_ring, CBDC_COM_ROWS);
  polyvec_mul (want, Acom, a0);
  polyvec_sub (want, want, com->nym, 0);
  polyvec_redc (want, want);
  polyvec_redc (t, t);
  if (polyvec_eq (want, t) != 1)
    ok = 0;

  polyvec_free (want);
  polyvec_free (a0);
  polyvec_free (t);
  polymat_free (Acom);
  polymat_free (A);
  cbdc_vec_free_secret (a);
  return ok;
}
