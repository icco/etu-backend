# @icco/etu-proto

Generated TypeScript protobuf types for the etu API.

## Installation

First, configure npm to use GitHub Packages for the `@icco` scope by adding to your `.npmrc`:

```
@icco:registry=https://npm.pkg.github.com
```

Then install:

```bash
npm install @icco/etu-proto
```

## Usage

```typescript
import { Note, Tag, NotesService } from '@icco/etu-proto';

// Use with @connectrpc/connect
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';

const transport = createConnectTransport({
  baseUrl: 'https://your-api.example.com',
});

const client = createClient(NotesService, transport);

const response = await client.listNotes({
  userId: 'user-123',
  limit: 10,
});
```

## Generated from

This package is automatically generated from the proto files in [icco/etu-backend](https://github.com/icco/etu-backend).
