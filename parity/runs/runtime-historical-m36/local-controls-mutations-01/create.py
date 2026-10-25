import pathlib,json,hashlib
root=pathlib.Path.cwd(); out=root/'parity/runs/runtime-historical-m36/local-controls-mutations-01'; source=root/'runtime/source_local_historical.go'; s=source.read_text()
launch='\t\tif !isLast {\n\t\t\tpending = launch(store.SourceInterval{math.Min(p.span[1]+1, lastNumber), math.Min(p.span[1]+1+estimate, lastNumber)})\n\t\t}\n'
assert s.count(launch)==1
seq=s.replace(launch,''); anchor='\t\tif isLast {\n\t\t\to.Progress.Current = last\n'; assert seq.count(anchor)==1; seq=seq.replace(anchor,launch+anchor)
err='\t\tif p.err != nil {\n\t\t\treturn p.err\n\t\t}\n'; assert s.count(err)==1
start='err := o.Store.WithTransaction(owned, func(bound *store.SourceSyncStore) error {'; assert s.count(start)==1
bypass=s.replace(start,'err := func(bound *store.SourceSyncStore) error {'); end='return bound.InsertIntervals(owned, o.ChainID, p.required, p.factories)\n\t\t\t})'; assert bypass.count(end)==1; bypass=bypass.replace(end,'return bound.InsertIntervals(owned, o.ChainID, p.required, p.factories)\n\t\t\t}(o.Store)')
mutants={'sequential':(seq,'range_RPC_prefetch_and_ordered_commits'),'swallow_error':(s.replace(err,''),'prefetch_error_preserves_prior_interval'),'unbound':(bypass,'interval_failure_rolls_back_rows')}
manifest={'original_sha256':hashlib.sha256(s.encode()).hexdigest(),'test_sha256':hashlib.sha256((root/'runtime/source_local_controls_test.go').read_bytes()).hexdigest(),'mutants':{}}
for name,(body,test) in mutants.items():
 d=out/name; d.mkdir(); target=d/'source_local_historical.go'; target.write_text(body); (d/'overlay.json').write_text(json.dumps({'Replace':{str(source):str(target)}},indent=2)+'\n'); manifest['mutants'][name]={'sha256':hashlib.sha256(body.encode()).hexdigest(),'test':test}
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
