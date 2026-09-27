"""Apply a BSDIFF40 release delta to a staging file using only the standard library.

Usage: python3 apply-binary-delta.py BASE PATCH OUTPUT BASE_SHA256 OUTPUT_SHA256
The live executable is never modified; deployment still uses upgrade-docker-cp.sh.
"""
import bz2
import hashlib
import pathlib
import sys


def integer(data):
    value = int.from_bytes(data, "little")
    return -(value & ((1 << 63) - 1)) if value >> 63 else value


def apply():
    base_path, patch_path, output_path, base_sha, output_sha = sys.argv[1:]
    base = pathlib.Path(base_path).read_bytes()
    if hashlib.sha256(base).hexdigest() != base_sha.lower():
        raise ValueError("Base binary SHA256 mismatch")
    patch = pathlib.Path(patch_path).read_bytes()
    if patch[:8] != b"BSDIFF40":
        raise ValueError("Unsupported patch format")
    control_size, diff_size, size = (integer(patch[i:i + 8]) for i in (8, 16, 24))
    if min(control_size, diff_size, size) < 0 or size > 256 * 1024 * 1024:
        raise ValueError("Invalid patch sizes")
    control = bz2.decompress(patch[32:32 + control_size])
    diff = bz2.decompress(patch[32 + control_size:32 + control_size + diff_size])
    extra = bz2.decompress(patch[32 + control_size + diff_size:])
    output = bytearray(size)
    old_pos = new_pos = diff_pos = extra_pos = 0
    for pos in range(0, len(control), 24):
        add, copy, seek = (integer(control[pos + i:pos + i + 8]) for i in (0, 8, 16))
        if min(add, copy) < 0 or new_pos + add + copy > size or diff_pos + add > len(diff) or extra_pos + copy > len(extra):
            raise ValueError("Invalid patch control")
        output[new_pos:new_pos + add] = diff[diff_pos:diff_pos + add]
        start, end = max(0, -old_pos), min(add, len(base) - old_pos)
        for i in range(start, end):
            output[new_pos + i] = (output[new_pos + i] + base[old_pos + i]) & 255
        new_pos += add
        old_pos += add + seek
        diff_pos += add
        output[new_pos:new_pos + copy] = extra[extra_pos:extra_pos + copy]
        new_pos += copy
        extra_pos += copy
    if new_pos != size or hashlib.sha256(output).hexdigest() != output_sha.lower():
        raise ValueError("Reconstructed binary SHA256 mismatch")
    with pathlib.Path(output_path).open("xb") as target:
        target.write(output)
    print("Staged binary SHA256 verified:", output_sha, flush=True)


if __name__ == "__main__":
    apply()
