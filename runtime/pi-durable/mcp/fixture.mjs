// Private loopback real-MCP transport fixture. No credentials or live servers.
import { createServer } from 'node:http';
import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { ListToolsRequestSchema, CallToolRequestSchema } from '@modelcontextprotocol/sdk/types.js';
export async function privateMcpServer() {
  const calls=[],requests=[],active=new Set(); let mode='normal';
  const http=createServer(async(req,res)=>{
    if(req.method!=='POST'){res.statusCode=405;res.end();return;}
    const protocol=new Server({name:'private-wt-mcp',version:'1.0.0'},{capabilities:{tools:{}}});
    active.add(protocol);
    protocol.setRequestHandler(ListToolsRequestSchema,async()=>({tools:[{
      name:'echo',description:'Private synthetic echo',inputSchema:{type:'object',properties:{text:{type:'string'}},required:['text'],additionalProperties:false},
    }]}));
    protocol.setRequestHandler(CallToolRequestSchema,async request=>{
      calls.push(request.params);
      if(mode==='drop'){res.destroy();throw new Error('private transport drop after effect');}
      return request.params.arguments.text==='failure'
        ? {content:[{type:'text',text:'PRIVATE_TOOL_ERROR'}],isError:true}
        : {content:[{type:'text',text:`PRIVATE_ECHO:${request.params.arguments.text}`}]};
    });
    const transport=new StreamableHTTPServerTransport({sessionIdGenerator:undefined,enableJsonResponse:true});
    try {
      const chunks=[];for await(const chunk of req)chunks.push(chunk);
      const body=JSON.parse(Buffer.concat(chunks).toString('utf8'));requests.push(body.method);
      await protocol.connect(transport);
      res.on('close',()=>{void protocol.close();active.delete(protocol);});
      await transport.handleRequest(req,res,body);
    } catch {if(!res.headersSent&&!res.destroyed){res.statusCode=500;res.end('private fixture error');}await protocol.close();active.delete(protocol);}
  });
  await new Promise(resolve=>http.listen(0,'127.0.0.1',resolve));
  return {calls,requests,url:new URL(`http://127.0.0.1:${http.address().port}/mcp`),setMode:value=>{mode=value;},
    async close(){await Promise.all([...active].map(protocol=>protocol.close()));http.closeAllConnections();await new Promise(resolve=>http.close(resolve));},
  };
}
