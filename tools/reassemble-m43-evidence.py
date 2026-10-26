#!/usr/bin/env python3
"""Verify and optionally reassemble lossless evidence chunks; never extract tar members."""
import argparse
import hashlib
import json
import os
from pathlib import Path

def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('manifest', type=Path)
    parser.add_argument('--verify-only', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    manifest = json.loads(args.manifest.read_text())
    if manifest['format'] != 'ponder-evidence-chunks-v1':
        raise ValueError('Unsupported manifest')
    def safe(name):
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError('Noncanonical evidence path')
        path = root / relative
        if path.resolve() != path:
            raise ValueError('Symlink or noncanonical evidence path')
        return path
    total = 0
    combined = hashlib.sha256()
    chunks = []
    for entry in manifest['chunks']:
        path = safe(entry['file'])
        if entry['bytes'] > 48 * 1024 * 1024 or path.stat().st_size != entry['bytes'] or digest(path) != entry['sha256']:
            raise ValueError('Chunk differs: ' + entry['file'])
        with path.open('rb') as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                combined.update(block)
                total += len(block)
        chunks.append(path)
    if total != manifest['original_bytes'] or combined.hexdigest() != manifest['original_sha256']:
        raise ValueError('Combined archive differs')
    target = safe(manifest['original_file'])
    if target.exists():
        if target.stat().st_size != total or digest(target) != combined.hexdigest():
            raise ValueError('Existing archive differs; refusing overwrite')
    elif not args.verify_only:
        # Exclusive creation: never overwrite an existing archive. A failed partial
        # output is deliberately retained for diagnosis rather than silently deleted.
        with target.open('xb') as output:
            for path in chunks:
                with path.open('rb') as stream:
                    for block in iter(lambda: stream.read(1024 * 1024), b''):
                        output.write(block)
            output.flush()
            os.fsync(output.fileno())
        if digest(target) != combined.hexdigest():
            raise ValueError('Reassembled output differs')
    print(json.dumps({'verified': True, 'bytes': total, 'sha256': combined.hexdigest(), 'archive_present': target.exists(), 'tar_extracted': False}))

if __name__ == '__main__':
    main()
