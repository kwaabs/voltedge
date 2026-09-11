-- 0049_create_diagram_templates.sql
-- Purpose: runtime.diagram_templates -- a small library of reusable diagram
-- layout skeletons (Phase 24 Slice 4, docs/phase-24-diagram-templates.md).
--
-- "Diagrams need templates" meant, among other things, a *layout skeleton* you
-- apply to a fresh diagram and then fill in. A template is authored by "save as
-- template" from an existing diagram: every placed real feature becomes an
-- empty, class-tagged `slot` (the feature id is deliberately dropped -- a
-- template is portable and binds to different real features each time it is
-- applied), and every non-feature element (box/text/line/symbol/title_block/
-- legend) is copied verbatim. Applying a template instantiates those slots and
-- furniture as runtime.diagram_elements rows on the target diagram; the user
-- then clicks each slot to bind it to a real feature (which materialises a
-- normal placement), the "manual slot binding" this phase locked in.
--
-- The whole skeleton is stored as a single jsonb `payload` ({ "elements": [...] }
-- of diagram_element shapes) rather than a normalised child table: a template
-- is opaque, written whole and read whole, never queried element-by-element, so
-- a child table would buy nothing. Slots inside the payload carry `slot_class`
-- as the human class code (not a feature_classes FK), same portable
-- code-not-uuid convention diagram_elements.slot_class itself uses.
--
-- Lives in runtime (not metadata): a template is user-authored at runtime from
-- real diagrams, not admin-compiled configuration like metadata.diagram_stencils.

CREATE TABLE runtime.diagram_templates (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    description text,
    payload     jsonb NOT NULL DEFAULT '{"elements": []}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE runtime.diagram_templates IS 'Reusable diagram layout skeletons (Phase 24 Slice 4): a library of saved slot+furniture sets that apply onto a fresh diagram. Authored by "save as template" (placements -> class-tagged slots, elements copied verbatim); applied by instantiating the payload as runtime.diagram_elements rows the user then binds. User-authored at runtime, hence runtime not metadata.';
COMMENT ON COLUMN runtime.diagram_templates.payload IS 'The skeleton as { "elements": [ {kind,x,y,width,height,rotation,path,text,style,z_index,slot_class,slot_subtype,slot_role}, ... ] }. Slots carry slot_class as a portable human class code, not a feature_classes FK. Read/written whole, never queried element-by-element.';
