 2. "Graph implementation -- any Postgres plugins or are we ok?"

  You're fine for now. Here's why:

  The entitystore already uses PostgreSQL with:
  - GIN-indexed text[] columns for token-based blocking
  - pgvector for embedding similarity
  - B-tree indexes on relation source/target IDs

  For the graph traversal patterns we use (FindConnectedByType, GetRelationsFrom/To, ConnectedEntities), these are 1-2 hop queries that PostgreSQL handles
  efficiently with indexed joins.

  When you'd need more:
  - Apache AGE (Postgres extension for openCypher) -- if you need multi-hop path queries like "find all entities within 3 hops" or "shortest path between Alice
  and some account". Not needed yet.
  - Recursive CTEs -- PostgreSQL already supports these natively. If you need "find all subsidiaries recursively" (walking the ownership tree), a recursive CTE
  in the entitystore's query layer would handle it without any extension.
  - pg_graphql -- useful if you want a GraphQL API over the graph, but that's an API layer concern, not a storage concern.

  Recommendation: Stay with plain PostgreSQL for now. Add recursive CTE queries to the entitystore when you need multi-hop traversal (e.g., "find all entities
  in Alice's network within 3 degrees"). That's a query method addition, not an architecture change.

