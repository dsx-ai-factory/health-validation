import React, { useState } from "react";
import { usePluginData } from "@docusaurus/useGlobalData";
import Chip from "../Chip";
import styles from "./styles.module.css";

interface EnumValue {
  name: string;
  json: string;
  description: string;
}

interface EnumDef {
  description: string;
  values: EnumValue[];
}

interface FieldDef {
  name: string;
  type: string;
  json?: string;
  description?: string;
}

interface StructDef {
  description: string;
  fields: FieldDef[];
}

interface Schema {
  enums: Record<string, EnumDef>;
  structs: Record<string, StructDef>;
}

// Known enum/struct names for cross-linking
function TypeLink({
  type,
  knownTypes,
}: {
  type: string;
  knownTypes: Set<string>;
}) {
  // Strip [] prefix and map[] wrapper to find the base type
  const baseMatch = type.match(/(?:\[])?(?:map\[.*?])?(\w+)$/);
  const baseType = baseMatch?.[1];
  if (baseType && knownTypes.has(baseType)) {
    return (
      <a href={`#${baseType.toLowerCase()}`}>
        <code>{type}</code>
      </a>
    );
  }
  return <code>{type}</code>;
}

function EnumSection({
  name,
  def,
  defaultOpen,
}: {
  name: string;
  def: EnumDef;
  defaultOpen: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className={styles.card} id={name.toLowerCase()}>
      <button
        className={styles.cardHeader}
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <Chip label="enum" variant="purple" small />
        <span className={styles.typeName}>{name}</span>
        <span className={styles.chevron}>{open ? "\u25B2" : "\u25BC"}</span>
      </button>
      {open && (
        <div className={styles.cardBody}>
          <p className={styles.typeDesc}>{def.description}</p>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>JSON Value</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {def.values.map((v) => (
                <tr key={v.name}>
                  <td>
                    <code>{v.name}</code>
                  </td>
                  <td>
                    <Chip label={v.json} variant={v.json} small />
                  </td>
                  <td>{v.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function StructSection({
  name,
  def,
  knownTypes,
  defaultOpen,
}: {
  name: string;
  def: StructDef;
  knownTypes: Set<string>;
  defaultOpen: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className={styles.card} id={name.toLowerCase()}>
      <button
        className={styles.cardHeader}
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <Chip label="struct" variant="teal" small />
        <span className={styles.typeName}>{name}</span>
        <span className={styles.fieldCount}>
          {def.fields.length} field{def.fields.length !== 1 ? "s" : ""}
        </span>
        <span className={styles.chevron}>{open ? "\u25B2" : "\u25BC"}</span>
      </button>
      {open && (
        <div className={styles.cardBody}>
          <p className={styles.typeDesc}>{def.description}</p>
          <table>
            <thead>
              <tr>
                <th>Field</th>
                <th>Type</th>
                <th>JSON Key</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {def.fields.map((f) => (
                <tr key={f.name}>
                  <td>
                    <code>{f.name}</code>
                  </td>
                  <td>
                    <TypeLink type={f.type} knownTypes={knownTypes} />
                  </td>
                  <td>{f.json && <code>{f.json}</code>}</td>
                  <td>{f.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export default function SchemaReference() {
  const { schema } = usePluginData("health-data") as { schema: Schema };

  const knownTypes = new Set([
    ...Object.keys(schema.enums),
    ...Object.keys(schema.structs),
  ]);

  const enumEntries = Object.entries(schema.enums).sort(([a], [b]) =>
    a.localeCompare(b),
  );
  const structEntries = Object.entries(schema.structs).sort(([a], [b]) =>
    a.localeCompare(b),
  );

  return (
    <div className={styles.schema}>
      <h2>Enums</h2>
      <div className={styles.typeList}>
        {enumEntries.map(([name, def]) => (
          <EnumSection key={name} name={name} def={def} defaultOpen={false} />
        ))}
      </div>

      <h2>Types</h2>
      <div className={styles.typeList}>
        {structEntries.map(([name, def]) => (
          <StructSection
            key={name}
            name={name}
            def={def}
            knownTypes={knownTypes}
            defaultOpen={false}
          />
        ))}
      </div>
    </div>
  );
}
