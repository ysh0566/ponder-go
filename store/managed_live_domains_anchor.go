package store

func (d *ManagedLiveDomains) validateIntentAnchor(intent liveDomainIntent, op managedLiveOperation) error {
	var control ManagedLiveControl
	switch op.Kind {
	case "open":
		if decodeLiveJSON(op.Payload, &control) != nil {
			return ErrManagedLiveReceiptConflict
		}
	case "batch":
		var e managedLiveBatchEvidence
		if decodeLiveJSON(op.Payload, &e) != nil {
			return ErrManagedLiveReceiptConflict
		}
		control = e.AfterControl
	case "reorg":
		var e managedLiveReorgEvidence
		if decodeLiveJSON(op.Payload, &e) != nil {
			return ErrManagedLiveReceiptConflict
		}
		control = e.AfterControl
	case "finalize":
		var e managedLiveFinalizeEvidence
		if decodeLiveJSON(op.Payload, &e) != nil {
			return ErrManagedLiveReceiptConflict
		}
		control = e.AfterControl
	case "serve":
		var e managedRuntimeServe
		if decodeLiveJSON(op.Payload, &e) != nil {
			return ErrManagedLiveReceiptConflict
		}
		control = e.After
	case "recover":
		var e managedLiveRecoveryEvidence
		if decodeLiveJSON(op.Payload, &e) != nil {
			return ErrManagedLiveReceiptConflict
		}
		control = e.AfterControl
	default:
		return ErrManagedLiveReceiptConflict
	}
	normalized, err := normalizeDomainReorg(d.live().opts, intent.Request)
	r := intent.Request.Reorg
	if err != nil || managedHash(normalized) != managedHash(intent.Request) || managedHash(control) != managedHash(intent.Before) || intent.Owner != op.OwnerToken || r.Generation != control.Generation || r.ExpectedLastOperation != control.LastOperation || r.ExpectedTail != control.Tail || intent.RequestHash != liveReorgHash(intent.Identity, intent.Owner, r) {
		return ErrManagedLiveReceiptConflict
	}
	if !runtimeReorgDeletedAllowed(d.live().opts, intent.Request, intent.Deleted) {
		return ErrManagedLiveReceiptConflict
	}
	return nil
}
