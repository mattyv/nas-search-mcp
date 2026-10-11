# Follow-up work

- [ ] Calibrate semantic relevance filtering on a representative document/code collection. Expose semantic distance separately from rank-fusion scores, test unrelated queries, and avoid treating rank-fusion scores as similarity probabilities.
- [ ] Validate full-library indexing time, storage growth, and query latency before expanding beyond measured collections.
- [ ] Validate CLIP photo inference and memory use on the DS220+.
- [ ] Validate each desktop client's original-file/attachment capabilities; distinguish receiving bytes from actually interpreting scanned pages.
- [ ] Add a safe token-rotation command. Existing MCP requests already require bearer authentication; `/healthz` is deliberately public.
- [ ] Validate remote client access while preserving the work Tailscale account and exit node.
- [ ] Consider per-user authorization or OAuth if the service is shared beyond this single-user setup.
- [ ] Extend OCR languages and validate recognition quality on representative scans. English is the initial target.
