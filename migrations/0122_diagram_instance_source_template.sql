-- 0122_diagram_instance_source_template.sql
-- Purpose: let an auto-generated assembly single-line remember the template it
-- was built from, so it can be regenerated faithfully.
--
-- The assembly auto-diagram builder (api/internal/diagram/autobuild.go) stands up
-- a ready-to-read single-line when a fan-out engine (RMU, packaged substation,
-- pole/pad kit, switchgear panel) creates an assembly: it applies a named layout
-- template and binds each typed slot to a real fanned-out component. "Regenerate"
-- rebuilds that diagram from scratch -- but to rebuild it truthfully (not by
-- guessing which of the eleven templates made it, nor by re-deriving members from
-- a network where some components attach by no structural edge at all, e.g. the
-- URD feeder pillar), the instance must record WHICH template built it. The
-- component list is read back from the instance's own placements; this column
-- supplies the missing half -- the layout.
--
-- Nullable by design: only auto-generated diagrams carry it. A hand-built diagram
-- (created via POST /diagrams, populated slot by slot) has no source template and
-- leaves this NULL -- regenerate refuses those, since there is nothing to rebuild
-- from. ON DELETE SET NULL so pruning a template from the library never orphan-
-- blocks or cascades into the diagrams it once produced; they simply lose the
-- ability to regenerate (the same graceful degradation as any missing template).
--
-- No data rewrite: every existing instance is a hand-built one and correctly
-- defaults to NULL. No recompile: this is a runtime table the Go model reads by
-- column, and DiagramInstance gains a matching nullzero field.

ALTER TABLE runtime.diagram_instances
    ADD COLUMN source_template_id uuid NULL
        REFERENCES runtime.diagram_templates (id) ON DELETE SET NULL;

COMMENT ON COLUMN runtime.diagram_instances.source_template_id IS
    'The diagram_templates row this instance was auto-generated from (assembly single-line builder). NULL for hand-built diagrams. Read by Regenerate to re-apply the same layout; SET NULL if that template is later deleted.';
