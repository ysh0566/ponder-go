// M44 supplements, never additional original-source test credit.
export const managedFiniteNativeFiles = [
  'historical/managed_test.go', 'historical/managed_plan_test.go',
  'indexing/client_managed_test.go', 'indexing/client_managed_sql_test.go',
  'indexing/client_managed_fallback_test.go', 'store/managed_finite_test.go',
];
export const managedFiniteDualBackendTests = [
  'TestManagedSQLCacheFailureCompletion',
  'TestManagedFiniteReadyAndResume', 'TestManagedFiniteRejectsUnmanagedAndIdentityChanges',
  'TestManagedFiniteFailureNeverReady', 'TestManagedFiniteCloseRetainsOwnership', 'TestManagedFiniteEscapedClientActivationBarrier',
  'TestManagedFiniteDeferredActivationAndRestart', 'TestManagedFiniteRejectsUnmarkedIndex',
  'TestManagedFiniteCatalogTamperFailsClosed', 'TestManagedFiniteCommitReconciliation',
  'TestManagedFiniteRestartRejectsUnsupportedState', 'TestManagedFiniteCanceledCommitStaysUnknown',
  'TestManagedFiniteNotificationCommitBoundary', 'TestManagedFiniteEveryActivationStageOutcome',
  'TestManagedFiniteEmptySchemaDistinctEnds', 'TestManagedFiniteFreshBoundaryBetweenStages',
];
export const managedFinitePostgresTests = ['TestManagedFinitePostgresControlLane', 'TestManagedFinitePostgresIdentityFences', 'TestManagedFinitePostgresExpiryInsideHandler'];
export const managedFinitePGliteTests = ['TestManagedFinitePGliteCrossProcessExclusion','TestManagedFinitePGliteBusyReadsAndRetainedClose'];
export const managedFiniteHarness = [
  'tools/managed-finite-acceptance.mjs', 'tools/managed-finite-evidence.mjs',
  'tools/managed-finite-preflight.mjs', 'tools/managed-finite-results.mjs',
  'tools/managed-finite-full-verify.mjs', 'tools/managed-finite-path-guards.test.mjs',
  'tools/managed-finite-observe.test.py', 'tools/managed-finite-observer.test.mjs', 'tools/managed-finite-observe.py', 'tools/managed-finite-launch.sh', 'tools/managed-finite-launch.mjs', 'tools/managed-finite-closeout.mjs',
];
export const managedFiniteCredit = Object.freeze({ source_credit: 0, ported_original: 543, original_total: 905 });

// Exact approved mutable derived outputs; never a blanket native/source exclusion.
export const managedFiniteDerivedOutputs = [
 'source-sync','source-historical','source-realtime','config-live','sql-source',
 'source-runtime-index','source-runtime-historical','source-runtime-multichain',
 'source-indexing-service','source-indexing-actions','source-indexing-profile',
 'independent-storage','multichain-atomic','finite-historical',
].map(name=>'parity/runs/'+name+'-verification.json').concat(['parity/test-ledger.json','parity/runs/summary.json']);
