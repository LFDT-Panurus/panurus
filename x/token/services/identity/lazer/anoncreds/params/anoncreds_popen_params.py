# Create a header file with LNP proof system parameters for
# proving knowledge of a witness w in Rp^n (Rp = Zp[X]/(X^d + 1))
# such that
#
#   1. w satisfies a linear relation over Rp: Aw + t = 0
#   2. each element in a partition of w either ..
#      2.1 has binary coefficients only
#      2.2 satisfies an l2-norm bound

vname = "popen_param"       # variable name

deg   = 64               # ring Rp degree d
mod   = 12289             # ring Rp modulus p
dim   = (2,1+10)             # dimensions of A in Rp^(m,n)

wpart = [ list(range(0,1)), list(range(1,11)) ]  # partition of w: [attr,r]
wl2   = [   64,     39 ]  # l2-norm bounds: l2(attr) <= 64, l2(attrs) <= 39
wbin  = [     0,     0 ]
#wrej  = [     0,     1 ]  # rejection sampling: on m only

# Optional: some linf-norm bound on w.
# Tighter bounds result in smaller proofs.
# If not specified, the default is the naive bound max(1,floor(max(wl2))).
wlinf = 8 # optional linf: some linf-norm bound on w.
