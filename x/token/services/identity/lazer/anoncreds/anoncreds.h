/*
 * Derived from the lazer library (https://github.com/lazer-crypto/lazer),
 * Copyright (c) 2022-2026 IBM, MIT license, see LICENSE.lazer.
 */

#ifndef ANONCREDS_H
#define ANONCREDS_H

/*
 * CBDC anonymous credential scheme on top of lazer. Needs lazer.h and
 * liblazer.a, installed by `make lazer-install` in the root of the panurus
 * repository.
 */
#include <stddef.h>
#include <stdint.h>

#include "lazer.h"

#define CBDC_ATTRLEN 32 /* byte-length of an attribute */
#define CBDC_NATTRS 5   /* number of attributes */
#define CBDC_COM_ROWS 2 /* rows of the commitment key Acom, see p2/popen params */
#define CBDC_MAXCOM 3   /* max number of committed attributes in a presentation */

/*
 * fixed encoding lengths (bytes): 14 bits per coefficient, plus the
 * terminator bit that coder_enc_end appends (one extra byte)
 */
#define CBDC_ENCLEN(npolys) ((npolys) * CBDC_DEG * CBDC_LOGMOD / 8 + 1)
#define CBDC_NYMLEN CBDC_ENCLEN (CBDC_COM_ROWS)
#define CBDC_OPENINGLEN CBDC_ENCLEN (10)  /* r, 10 elems */
#define CBDC_USERSTATELEN CBDC_ENCLEN (16) /* r, 16 elems */

/*
 * Squared l2 norm bounds of the witnesses. They must equal the bounds the
 * proof systems were generated with (the scripts in params), which cbdc_params_ok
 * checks against anoncreds_params.h.
 */
#define CBDC_L2SQR_BND_R (109 * 109)   /* masking randomness r, P1 and P2 */
#define CBDC_L2SQR_BND_ATTRS (91 * 91) /* encoded attributes, P1 */
#define CBDC_L2SQR_BND_CRED 34034726   /* Falcon preimage (s1,s2), P2 */
#define CBDC_L2SQR_BND_ATTR (64 * 64)  /* one encoded attribute, P2, SoK */
#define CBDC_L2SQR_BND_COM (39 * 39)   /* commitment randomness, P2, SoK */

/* ring Rp = Z_12289[X]/(X^64+1) */
#define CBDC_DEG 64
#define CBDC_MOD 12289
#define CBDC_LOGMOD 14 /* bit length of CBDC_MOD */

/* state of the CBDC scheme, see anoncreds.c */
typedef struct
{
  polymat_t B2; /* Falcon public key as 8x8 matrix over the 64-deg ring */
  polyvec_t c0; /* public non-zero target derived from the public key, 8 elems */
  lin_prover_state_t p1;
  lin_prover_state_t p2;
  polyvec_t r; /* masking randomness, 16 elems */
  polyvec_t m; /* encoded attributes, 5 elems */
  int masked;  /* set once cbdc_user_maskattrs or cbdc_user_import has run */
  int ready;   /* set by a successful cbdc_user_init or cbdc_user_template_init */
  int p1_ready; /* p1 is initialized (cbdc_user_init) */
  int p2_ready; /* p2 is initialized (cbdc_user_init, cbdc_user_clone) */
  int template; /* set by cbdc_user_template_init: never proves, see there */
} cbdc_user_state_struct;
typedef cbdc_user_state_struct cbdc_user_state_t[1];
typedef cbdc_user_state_struct *cbdc_user_state_ptr;

typedef struct
{
  uint8_t sk[1281];
  polyvec_t c0; /* see cbdc_user_state_struct */
  lin_verifier_state_t p1;
  int ready; /* set by a successful cbdc_issuer_init */
} cbdc_issuer_state_struct;
typedef cbdc_issuer_state_struct cbdc_issuer_state_t[1];
typedef cbdc_issuer_state_struct *cbdc_issuer_state_ptr;

typedef struct
{
  polymat_t B2;
  polyvec_t c0; /* see cbdc_user_state_struct */
  lin_verifier_state_t p2;
  int ready; /* set by a successful cbdc_verifier_init */
} cbdc_verifier_state_struct;
typedef cbdc_verifier_state_struct cbdc_verifier_state_t[1];
typedef cbdc_verifier_state_struct *cbdc_verifier_state_ptr;

/* commitment nym = A*(attr,r) to an attribute, with its opening */
typedef struct
{
  polyvec_t nym; /* 2 elems */
  polyvec_t r;   /* 10 elems */
} cbdc_commit_struct;
typedef cbdc_commit_struct cbdc_commit_t[1];
typedef cbdc_commit_struct *cbdc_commit_ptr;
typedef const cbdc_commit_struct *cbdc_commit_srcptr;

/*
 * Output buffers passed to the functions below must hold at least the
 * corresponding cbdc_maxlen_* bytes; nothing is written past them, a result
 * that would not fit makes the function fail. Functions returning int return
 * 1 on success and 0 on failure (malformed input, a proof that does not
 * verify, a state that is not initialized).
 */
int cbdc_params_ok (void);
int cbdc_params_check (void);
size_t cbdc_maxlen_masked_attrs (void);
size_t cbdc_maxlen_cred (void);
size_t cbdc_maxlen_pres (void);
size_t cbdc_maxlen_nym (void);
size_t cbdc_maxlen_opening (void);
size_t cbdc_maxlen_userstate (void);
size_t cbdc_maxlen_sok (void);

void cbdc_issuer_keygen (uint8_t sk[1281], uint8_t pk[897]);

int cbdc_pubkey_ok (const uint8_t pubkey[897]);
int cbdc_user_init (cbdc_user_state_t state, const uint8_t pubkey[897]);
void cbdc_user_clear (cbdc_user_state_t state);
int cbdc_user_commit (cbdc_commit_t com, uint8_t *nym, size_t *nymlen,
                      const uint8_t attr[32]);
void cbdc_commit_clear (cbdc_commit_t com);
int cbdc_commit_export (cbdc_commit_t com, uint8_t *opening,
                        size_t *openinglen);
int cbdc_commit_import (cbdc_commit_t com, uint8_t *nym, size_t *nymlen,
                        const uint8_t attr[32], const uint8_t *opening,
                        size_t openinglen);
int cbdc_opening_verify (const uint8_t *nym, size_t nymlen,
                         const uint8_t attr[32], const uint8_t *opening,
                         size_t openinglen);
int cbdc_user_sokprove (uint8_t *proof, size_t *proofl, cbdc_commit_t com,
                        const uint8_t *ctx, size_t ctxlen,
                        const uint8_t attr[32]);
int cbdc_user_maskattrs (cbdc_user_state_t state, uint8_t *masked_attrs,
                         size_t *masked_attrslen, const uint8_t *attrs,
                         const unsigned int *idx_rev, unsigned int nrev);
int cbdc_user_presgen (cbdc_user_state_t state, uint8_t *pres, size_t *preslen,
                       const uint8_t *cred, size_t credlen,
                       const unsigned int *idx_rev, unsigned int nrev,
                       cbdc_commit_ptr *coms, const unsigned int *idx_com,
                       unsigned int ncom, const uint8_t *ctx, size_t ctxlen);
int cbdc_user_credverify (cbdc_user_state_t state, const uint8_t *cred,
                          size_t credlen);
int cbdc_user_export (cbdc_user_state_t state, uint8_t *out, size_t *outlen);
int cbdc_user_import (cbdc_user_state_t state, const uint8_t *in, size_t inlen,
                      const uint8_t *attrs);
int cbdc_user_template_init (cbdc_user_state_t state,
                             const uint8_t pubkey[897]);
int cbdc_user_clone (cbdc_user_state_t dst, const cbdc_user_state_struct *src);
int cbdc_attrs_ok (const uint8_t *attrs);

int cbdc_issuer_init (cbdc_issuer_state_t state, const uint8_t pubkey[897],
                      const uint8_t privkey[1281]);
void cbdc_issuer_clear (cbdc_issuer_state_t state);
int cbdc_issuer_issue (cbdc_issuer_state_t state, uint8_t *cred,
                       size_t *credlen, const uint8_t *attrs_rev,
                       const unsigned int *idx_rev, unsigned int nrev,
                       const uint8_t *masked_attrs, size_t masked_attrslen);

int cbdc_verifier_init (cbdc_verifier_state_t state,
                        const uint8_t pubkey[897]);
void cbdc_verifier_clear (cbdc_verifier_state_t state);
int cbdc_verifier_presver (cbdc_verifier_state_t state,
                           const uint8_t *attrs_rev,
                           const unsigned int *idx_rev, unsigned int nrev,
                           const uint8_t *const *nyms, const size_t *nymlens,
                           const unsigned int *idx_com, unsigned int ncom,
                           const uint8_t *pres, size_t preslen,
                           const uint8_t *ctx, size_t ctxlen);
int cbdc_verifier_sokverify (const uint8_t *nym, size_t nymlen,
                             const uint8_t *ctx, size_t ctxlen,
                             const uint8_t *proof, size_t proofl);

/* for tests only, see anoncreds.c */
size_t cbdc_proof_hint_offset (int which);
int cbdc_test_grandom (int64_t *out, unsigned int n, unsigned int log2o,
                       const uint8_t seed[32], uint32_t dom);
int cbdc_test_p2_witness_wiped (cbdc_user_state_t state);

#endif
