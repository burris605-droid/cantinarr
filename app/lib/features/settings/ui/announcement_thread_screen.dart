import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

class AnnouncementThreadScreen extends ConsumerStatefulWidget {
  const AnnouncementThreadScreen({super.key,required this.endpoint,required this.heading,required this.admin,required this.canReply});
  final String endpoint,heading;
  final bool admin,canReply;
  @override
  ConsumerState<AnnouncementThreadScreen> createState()=>_AnnouncementThreadState();
}
class _AnnouncementThreadState extends ConsumerState<AnnouncementThreadScreen> {
  final text=TextEditingController();
  List<Map<String,dynamic>> replies=[];
  bool busy=false,loading=true;
  String? error;
  @override
  void initState(){super.initState();load();}
  @override
  void dispose(){text.dispose();super.dispose();}
  Future<void> load()async{
    try{
      final r=await ref.read(backendClientProvider).get(widget.endpoint);
      if(mounted)setState((){replies=(r.data as List).map((x)=>Map<String,dynamic>.from(x as Map)).toList();loading=false;error=null;});
    }catch(_){if(mounted)setState((){loading=false;error='Conversation unavailable.';});}
  }
  Future<void> send()async{
    if(busy||!widget.canReply||text.text.trim().isEmpty)return;
    setState(()=>busy=true);
    try{
      await ref.read(backendClientProvider).post(widget.endpoint,data:{'body':text.text.trim()});
      if(mounted){text.clear();await load();}
    }catch(_){if(mounted)setState(()=>error='Reply not confirmed. Refresh before retrying.');}
    finally{if(mounted)setState(()=>busy=false);}
  }
  @override
  Widget build(BuildContext context)=>Scaffold(appBar:AppBar(title:Text(widget.heading),actions:[IconButton(onPressed:busy?null:load,icon:const Icon(Icons.refresh))]),body:Center(child:ConstrainedBox(constraints:const BoxConstraints(maxWidth:760),child:Column(children:[
    const Padding(padding:EdgeInsets.all(12),child:Text('Private conversation between this recipient and the server administrators. Replies appear here, not as phone pushes.')),
    if(error!=null)Text(error!),
    if(loading)const LinearProgressIndicator(),
    Expanded(child:ListView(padding:const EdgeInsets.all(12),children:[
      if(!loading&&replies.isEmpty)const Center(child:Text('No replies yet.')),
      for(final r in replies)Align(alignment:(r['from_admin']==true)==widget.admin?Alignment.centerRight:Alignment.centerLeft,child:Card(child:Padding(padding:const EdgeInsets.all(12),child:Column(crossAxisAlignment:CrossAxisAlignment.start,mainAxisSize:MainAxisSize.min,children:[
        Text(r['from_admin']==true?'Administrator':'Recipient',style:Theme.of(context).textTheme.labelMedium),
        SelectableText('${r['body']}'),
        Text(DateTime.fromMillisecondsSinceEpoch((r['created'] as num).toInt()*1000,isUtc:true).toLocal().toString().split('.').first,style:Theme.of(context).textTheme.bodySmall),
      ])))),
    ])),
    if(widget.canReply)SafeArea(top:false,child:Padding(padding:const EdgeInsets.all(12),child:Row(crossAxisAlignment:CrossAxisAlignment.end,children:[
      Expanded(child:TextField(controller:text,enabled:!busy,maxLength:1000,minLines:1,maxLines:4,decoration:const InputDecoration(labelText:'Private reply'),onChanged:(_)=>setState((){}))),
      IconButton(onPressed:busy||text.text.trim().isEmpty?null:send,icon:const Icon(Icons.send),tooltip:'Send reply'),
    ])))else const Padding(padding:EdgeInsets.all(12),child:Text('Replies are disabled.')),
  ]))));
}
