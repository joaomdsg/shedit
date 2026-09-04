// Custom markdownlint rule: forbid tables.
module.exports = {
  names: ["no-tables"],
  description: "Tables are not allowed",
  tags: ["tables"],
  parser: "micromark",
  function: (params, onError) => {
    for (const token of params.parsers.micromark.tokens) {
      if (token.type === "table") {
        onError({ lineNumber: token.startLine, detail: "Use lists or prose instead of a table" });
      }
    }
  },
};
