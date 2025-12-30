import utime

def tak(x,y,z):
    if x <= y:
        return z

    return tak(
        tak(x-1, y, z),
        tak(y-1, z, x),
        tak(z-1, x, y),
    )



total = 0
nanos = 0
rounds = 100

t0 = utime.ticks_cpu()
for r in range(rounds): total += tak(18, 12, 6)
nanos += utime.ticks_diff(utime.ticks_cpu(), t0)

print(total, nanos / rounds)
