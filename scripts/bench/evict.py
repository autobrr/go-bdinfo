import os, sys
def evict(p):
    try:
        fd = os.open(p, os.O_RDONLY)
        try: os.posix_fadvise(fd, 0, 0, os.POSIX_FADV_DONTNEED)
        finally: os.close(fd)
    except OSError as e: print("evict fail", p, e, file=sys.stderr)
root = sys.argv[1]
if os.path.isdir(root):
    for d, _, fs in os.walk(root):
        for f in fs: evict(os.path.join(d, f))
else: evict(root)
