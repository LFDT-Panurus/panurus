from math import sqrt

vname = "p2_param"                     # variable name

deg   = 64                           # ring Rp degree d
mod   = 12289                           # ring Rp modulus p
dim   = (8+3*2,40+5*1+3*10)               # dimensions of A in Rp^(m,n)

# partition of w : [r1,r2], [tau], [s1,s2], 5x[attr], 3x[rand]
wpart = [ list(range(0,16)), list(range(16,24)), list(range(24,40)), [40], [41], [42], [43], [44],  list(range(45,55)), list(range(55,65)), list(range(65,75))] 
# l2-norm bounds : l2(r1,r2) <= 109, l2(s1,s2) <= sqrt(34034726), 5x(l2(attr) <= 64), 3x(l2(r) <= 39)
wl2   = [109, 0, sqrt(34034726), 64, 64, 64, 64, 64, 39, 39, 39]
wbin  = [0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0 ]  # binary coeffs : tau is binary

# Optional: some linf-norm bound on w.
# Tighter bounds result in smaller proofs.
# If not specified, the default is the naive bound max(1,floor(max(wl2))).
wlinf = 5833  # optional linf: some linf-norm bound on w.
