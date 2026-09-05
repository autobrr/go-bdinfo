import resource, subprocess, sys, time
logf, cmd = sys.argv[1], sys.argv[3:]
t0 = time.monotonic()
with open(logf, "wb") as lf:
    rc = subprocess.call(cmd, stdout=lf, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL)
wall = time.monotonic() - t0
ru = resource.getrusage(resource.RUSAGE_CHILDREN)
print(f"{wall:.2f}\t{ru.ru_utime:.2f}\t{ru.ru_stime:.2f}\t{ru.ru_maxrss}\t{rc}")
