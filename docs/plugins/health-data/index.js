const path = require("path");
const fs = require("fs");

module.exports = function healthDataPlugin(context) {
  return {
    name: "health-data",
    async loadContent() {
      const staticDir = path.join(context.siteDir, "static", "data");
      const checksPath = path.join(staticDir, "checks.json");
      const schemaPath = path.join(staticDir, "schema.json");

      const checks = fs.existsSync(checksPath)
        ? JSON.parse(fs.readFileSync(checksPath, "utf-8"))
        : [];
      const schema = fs.existsSync(schemaPath)
        ? JSON.parse(fs.readFileSync(schemaPath, "utf-8"))
        : { enums: {}, structs: {} };

      return { checks, schema };
    },
    async contentLoaded({ content, actions }) {
      actions.setGlobalData(content);
    },
  };
};
