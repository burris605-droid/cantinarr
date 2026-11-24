import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../auth/data/auth_service.dart';
import 'inbox_management_screen.dart';
import 'announcement_link.dart';
import 'package:go_router/go_router.dart';

class AnnouncementsScreen extends ConsumerStatefulWidget {
  const AnnouncementsScreen({super.key, required this.users});
  final List<UserSummary> users;
  @override
  ConsumerState<AnnouncementsScreen> createState() => _AnnouncementsScreenState();
}

class _AnnouncementsScreenState extends ConsumerState<AnnouncementsScreen> {
  static const base = '/api/admin/announcements-v2';
  final title = TextEditingController(), body = TextEditingController(), search = TextEditingController();
  final selected = <int>{};
  String audience = 'self', mode = 'now', filter = 'all', priority='general';
  bool pinned=false, requireAck=false, inboxOnly=false;
  String? editing, requestKey, status, error;
  DateTime? when;
  int repeat = 0;
  bool busy = false, loading = true, refreshing = false;
  List<Map<String,dynamic>> jobs = [], legacy = [];
  Map<int,Map<String,dynamic>> devices = {};
  Timer? timer;
  @override
  void initState() {
    super.initState(); load();
    timer = Timer.periodic(const Duration(seconds: 8), (_) { if (!busy) load(); });
  }
  @override
  void dispose() { timer?.cancel(); title.dispose(); body.dispose(); search.dispose(); super.dispose(); }
  List<Map<String,dynamic>> maps(dynamic d) => (d as List).map((x)=>Map<String,dynamic>.from(x as Map)).toList();
  Future<void> load() async {
    if (refreshing) return;
    refreshing = true;
    try {
      final api=ref.read(backendClientProvider);
      final j=maps((await api.get('$base/')).data);
      final d=maps((await api.get('$base/recipients')).data);
      final l=maps((await api.get('/api/admin/announcements')).data);
      if (mounted) setState(() {jobs=j;legacy=l;devices={for(final v in d)(v['id'] as num).toInt():v};loading=false;error=null;});
    } catch (_) { if(mounted) setState((){loading=false;error='Unable to refresh announcement center.';}); }
    finally {refreshing=false;}
  }
  String keyForRequest() {final r=Random.secure();return List.generate(24,(_)=>r.nextInt(256).toRadixString(16).padLeft(2,'0')).join();}
  void changed() => setState(()=>requestKey=null);
  void reset() {title.clear();body.clear();selected.clear();editing=null;requestKey=null;when=null;repeat=0;mode='now';audience='self';priority='general';pinned=false;requireAck=false;inboxOnly=false;}
  String dateText(dynamic raw) {
    if(raw is! num || raw<=0)return '—';
    final d=DateTime.fromMillisecondsSinceEpoch(raw.toInt()*1000,isUtc:true).toLocal();
    return '${d.year}-${d.month.toString().padLeft(2,'0')}-${d.day.toString().padLeft(2,'0')} ${d.hour.toString().padLeft(2,'0')}:${d.minute.toString().padLeft(2,'0')}';
  }
  String audienceText(String a)=>switch(a){'all'=>'Everyone','admins'=>'Administrators','selected'=>'Selected users',_=>'Myself'};
  bool get valid=>title.text.trim().isNotEmpty&&body.text.trim().isNotEmpty&&(audience!='selected'||selected.isNotEmpty);
  Future<bool> confirm(String heading,String message,String action) async =>
    await showDialog<bool>(context:context,builder:(ctx)=>AlertDialog(title:Text(heading),content:Text(message),actions:[
      TextButton(onPressed:()=>Navigator.pop(ctx,false),child:const Text('Cancel')),
      FilledButton(onPressed:()=>Navigator.pop(ctx,true),child:Text(action)),
    ]))==true;
  Future<void> pickDate() async {
    final now=DateTime.now();
    final d=await showDatePicker(context:context,initialDate:when?.isAfter(now)==true?when!:now,firstDate:DateUtils.dateOnly(now),lastDate:now.add(const Duration(days:365)));
    if(!mounted||d==null)return;
    final t=await showTimePicker(context:context,initialTime:TimeOfDay.fromDateTime(when??now.add(const Duration(hours:1))));
    if(!mounted||t==null)return;
    setState((){when=DateTime(d.year,d.month,d.day,t.hour,t.minute);requestKey=null;});
  }
  Future<void> save(String kind) async {
    if(busy||!valid)return;
    if(kind=='scheduled'&&(when==null||!when!.isAfter(DateTime.now()))){setState(()=>status='Choose a future date and time.');return;}
    final k=editing??(requestKey??=keyForRequest());
    final ids=selected.toList()..sort();
    final data={'key':k,'mode':kind,'title':title.text.trim(),'body':body.text.trim(),'audience':audience,'user_ids':audience=='selected'?ids:<int>[],'run_at':kind=='scheduled'?when!.millisecondsSinceEpoch~/1000:0,'repeat_days':kind=='scheduled'?repeat:0,'priority':priority,'pinned':pinned,'require_ack':requireAck,'inbox_only':inboxOnly};
    setState((){busy=true;status=null;});
    try {
      if(kind=='now'||kind=='scheduled') {
        if(!await confirm(kind=='now'?'Send announcement?':'Schedule announcement?','${title.text}\n\n${body.text}\n\nTo: ${audienceText(audience)}${audience=='selected'?' (${ids.length})':''}\n\n${inboxOnly?'Inbox only: no phone alerts.':'Phone alerts respect push preferences.'} Inbox copies are saved for addressed users.',kind=='now'?'Send':'Schedule'))return;
      }
      if(!mounted)return;
      final api=ref.read(backendClientProvider);
      if(editing==null){await api.post('$base/',data:data);}else{await api.put('$base/$editing',data:data);}
      if(!mounted)return;
      setState((){reset();status=kind=='now'?'Queued. Watch the delivery report below.':'Saved successfully.';});
      await load();
    }catch(_){if(mounted)setState(()=>status='Save not confirmed. Refresh before changing or retrying. An unchanged retry uses the same request key.');}
    finally{if(mounted)setState(()=>busy=false);}
  }
  void reuse(Map<String,dynamic> j,{bool edit=false}) {
    setState((){
      reset();title.text='${j['title']}';body.text='${j['body']}';
      final a='${j['audience']}';audience=['all','self','selected','admins'].contains(a)?a:'self';
      final ids=j['user_ids'];if(ids is List)selected.addAll(ids.whereType<num>().map((n)=>n.toInt()));
      editing=edit?'${j['id']}':null;
      priority=['general','important','urgent','maintenance'].contains(j['priority'])?j['priority'] as String:'general';pinned=j['pinned']==true;requireAck=j['require_ack']==true;inboxOnly=j['inbox_only']==true;
      if(edit&&j['status']=='queued'&&(j['run_at'] as num? ?? 0)>DateTime.now().millisecondsSinceEpoch~/1000){
        mode='scheduled';when=DateTime.fromMillisecondsSinceEpoch((j['run_at'] as num).toInt()*1000,isUtc:true).toLocal();repeat=(j['repeat_days'] as num? ?? 0).toInt();
      }
      status=edit?'Editing saved item.':'Copied to composer. Review recipients before sending.';
    });
  }
  Future<void> remove(Map<String,dynamic>? j,{bool old=false}) async {
    if(busy)return;
    if(!await confirm(j==null?'Clear history?':'Delete this item?',j==null?'Completed history only. Schedules, drafts, templates and personal inboxes are preserved.':'Already delivered notifications and personal inbox copies are not removed.','Delete'))return;
    if(!mounted)return;
    setState(()=>busy=true);
    try {
      final api=ref.read(backendClientProvider);
      if(j==null){await api.delete('$base/history${old?'?older=30':''}');}else{await api.delete('$base/${j['id']}');}
      if(mounted&&j!=null&&editing==j['id'])setState(reset);
      if(mounted)await load();
    }catch(_){if(mounted)setState(()=>status='Delete failed. Refresh and try again.');}
    finally{if(mounted)setState(()=>busy=false);}
  }
  void template(String t,String b)=>setState((){reset();title.text=t;body.text=b;});
  bool matches(Map<String,dynamic> j) {
    final s='${j['status']}';
    if(filter=='scheduled'&&!['queued','running'].contains(s))return false;
    if(filter=='saved'&&!['draft','template'].contains(s))return false;
    if(filter=='history'&&['queued','running','draft','template'].contains(s))return false;
    return '${j['title']} ${j['body']} $s'.toLowerCase().contains(search.text.trim().toLowerCase());
  }
  Widget job(Map<String,dynamic> j) {
    final s='${j['status']}';final editable=['queued','draft','template'].contains(s);
    return Card(child:Padding(padding:const EdgeInsets.all(12),child:Column(crossAxisAlignment:CrossAxisAlignment.start,children:[
      Row(children:[Expanded(child:Text('${j['title']}',style:Theme.of(context).textTheme.titleMedium)),Chip(label:Text(s.replaceAll('_',' ')))]),
      Text('${j['body']}'),const SizedBox(height:8),Text('${audienceText('${j['audience']}')} · ${dateText(j['created'])}'),
      if(s=='queued')Text('Scheduled: ${dateText(j['run_at'])} · Repeat days: ${j['repeat_days']}'),
      if(!editable&&s!='running')Text('Inbox: ${j['recipients']} · Accepted: ${j['sent']} · Failed: ${j['failed']} · Skipped push: ${j['skipped']}'),
      if('${j['note']??''}'.isNotEmpty)Text('${j['note']}'),
      Wrap(spacing:8,children:[
        TextButton.icon(onPressed:busy?null:()=>reuse(j),icon:const Icon(Icons.copy),label:const Text('Copy')),
        if(editable)TextButton.icon(onPressed:busy?null:()=>reuse(j,edit:true),icon:const Icon(Icons.edit_outlined),label:const Text('Edit')),
        if(s!='running')TextButton.icon(onPressed:busy?null:()=>remove(j),icon:const Icon(Icons.delete_outline),label:Text(s=='queued'?'Cancel schedule':'Delete')),
      ]),
    ])));
  }
  Future<void> options() async {
    if(busy)return;
    setState(()=>busy=true);
    try{
      final api=ref.read(backendClientProvider);
      final r=await api.get('$base/options');
      final o=Map<String,dynamic>.from(r.data as Map);
      if(!mounted)return;
      var retention=o['retention']==true,welcome=o['welcome']==true;
      var welcomeTitle=o['title'] as String,welcomeBody=o['body'] as String;
      final form=GlobalKey<FormState>();
      final result=await showDialog<Map<String,dynamic>>(context:context,builder:(ctx)=>StatefulBuilder(builder:(ctx,update)=>AlertDialog(
        title:const Text('Notification automation'),
        content:SingleChildScrollView(child:Form(key:form,child:Column(mainAxisSize:MainAxisSize.min,children:[
          SwitchListTile(title:const Text('Auto-clear old V2 history'),subtitle:const Text('After 30 days. Personal inboxes and schedules stay intact.'),value:retention,onChanged:(v)=>update(()=>retention=v)),
          SwitchListTile(title:const Text('Automatic welcome'),subtitle:const Text('New accounts only, once they have enabled push. Existing accounts are excluded when enabled.'),value:welcome,onChanged:(v)=>update(()=>welcome=v)),
          TextFormField(initialValue:welcomeTitle,maxLength:100,decoration:const InputDecoration(labelText:'Welcome title'),onChanged:(v)=>welcomeTitle=v,validator:(v)=>v==null||v.trim().isEmpty?'Required':null),
          TextFormField(initialValue:welcomeBody,maxLength:500,minLines:2,maxLines:4,decoration:const InputDecoration(labelText:'Welcome message'),onChanged:(v)=>welcomeBody=v,validator:(v)=>v==null||v.trim().isEmpty?'Required':null),
        ]))),
        actions:[
          TextButton(onPressed:()=>Navigator.pop(ctx),child:const Text('Cancel')),
          FilledButton(onPressed:(){if(form.currentState?.validate()==true)Navigator.pop(ctx,{'retention':retention,'welcome':welcome,'title':welcomeTitle.trim(),'body':welcomeBody.trim()});},child:const Text('Save')),
        ],
      )));
      if(result==null||!mounted)return;
      await api.put('$base/options',data:result);
      if(mounted)setState(()=>status='Automation settings saved.');
    }catch(_){if(mounted)setState(()=>status='Could not save or load automation settings.');}
    finally{if(mounted)setState(()=>busy=false);}
  }

  @override
  Widget build(BuildContext context) {
    final theme=Theme.of(context);
    final eligible=devices.values.where((d)=>d['eligible']==true).length;
    final queued=jobs.where((j)=>['queued','running'].contains(j['status'])).length;
    final done=jobs.where((j)=>j['status']=='completed').length;
    return Scaffold(appBar:AppBar(title:const Text('Announcement center'),actions:[IconButton(onPressed:busy?null:options,icon:const Icon(Icons.settings_outlined),tooltip:'Automation settings'),IconButton(onPressed:busy?null:load,icon:const Icon(Icons.refresh),tooltip:'Refresh')]),
      body:Center(child:ConstrainedBox(constraints:const BoxConstraints(maxWidth:840),child:ListView(padding:const EdgeInsets.all(20),children:[
        Text('BURRIS EDITION · V2',style:theme.textTheme.labelLarge?.copyWith(color:theme.colorScheme.primary)),
        Text('Broadcast control',style:theme.textTheme.headlineMedium),
        FilledButton.tonalIcon(onPressed:busy?null:()=>Navigator.of(context).push(MaterialPageRoute<void>(builder:(_)=>InboxManagementScreen(users:widget.users))),icon:const Icon(Icons.inbox_outlined),label:const Text('Inbox management')),
        Wrap(spacing:8,children:[TextButton.icon(onPressed:()=>context.push('/settings/announcement-inbox'),icon:const Icon(Icons.mail_outline),label:const Text('My inbox')),TextButton.icon(onPressed:()=>copyAnnouncementInboxLink(context,'all users'),icon:const Icon(Icons.link),label:const Text('Copy inbox link'))]),
        Wrap(spacing:8,children:[Chip(label:Text('$eligible push-ready users')),Chip(label:Text('$queued queued')),Chip(label:Text('$done completed'))]),
        if(error!=null)Text(error!,style:TextStyle(color:theme.colorScheme.error)),
        const Text('Official mobile alerts open Cantinarr. Full messages are also available in the web inbox.'),
        const SizedBox(height:16),Text('Quick templates',style:theme.textTheme.titleMedium),
        Wrap(spacing:8,children:[
          ActionChip(label:const Text('Maintenance'),onPressed:busy?null:()=>template('Server maintenance','The media server will be temporarily offline. Thanks for your patience!')),
          ActionChip(label:const Text('New content'),onPressed:busy?null:()=>template('New movies & shows','Fresh entertainment has arrived. Enjoy!')),
          ActionChip(label:const Text('Welcome'),onPressed:busy?null:()=>template('Welcome aboard!','Welcome to our media server. Enjoy your favorite movies and shows!')),
          ActionChip(label:const Text('Back online'),onPressed:busy?null:()=>template('Server back online','Maintenance is complete. Happy watching!')),
        ]),
        const SizedBox(height:12),
        if(editing!=null)Row(children:[const Expanded(child:Text('Editing saved item')),TextButton(onPressed:busy?null:()=>setState(reset),child:const Text('Cancel edit'))]),
        DropdownButtonFormField<String>(key:ValueKey(audience),initialValue:audience,decoration:const InputDecoration(labelText:'Recipients'),items:const[
          DropdownMenuItem(value:'self',child:Text('Test to myself')),DropdownMenuItem(value:'all',child:Text('Everyone')),DropdownMenuItem(value:'admins',child:Text('Administrators')),DropdownMenuItem(value:'selected',child:Text('Selected users')),
        ],onChanged:busy?null:(v)=>setState((){audience=v??'self';requestKey=null;})),
        if(audience=='selected')...[
          Wrap(children:[TextButton(onPressed:busy?null:()=>setState((){selected.addAll(widget.users.map((u)=>u.id));requestKey=null;}),child:const Text('Select all')),TextButton(onPressed:busy?null:()=>setState((){selected.clear();requestKey=null;}),child:const Text('Clear selection'))]),
          for(final u in widget.users)CheckboxListTile(title:Text(u.username),subtitle:Text(devices[u.id]==null?'Status unavailable':'${devices[u.id]!['devices']} registered device(s) · ${devices[u.id]!['eligible']==true?'Push ready':'Inbox only'}'),value:selected.contains(u.id),onChanged:busy?null:(v)=>setState((){if(v==true){selected.add(u.id);}else{selected.remove(u.id);}requestKey=null;})),
        ],
        if(audience=='all')Text('$eligible users currently push-ready; other users receive inbox copies only.'),
        if(audience=='selected')Text('${selected.where((id)=>devices[id]?['eligible']==true).length} of ${selected.length} selected users currently push-ready.'),
        if(audience=='admins')Text('${widget.users.where((u)=>u.isAdmin&&devices[u.id]?['eligible']==true).length} administrators currently push-ready.'),
        const SizedBox(height:12),
        TextField(controller:title,enabled:!busy,maxLength:100,decoration:const InputDecoration(labelText:'Notification title'),onChanged:(_)=>changed()),
        TextField(controller:body,enabled:!busy,maxLength:500,minLines:3,maxLines:6,decoration:const InputDecoration(labelText:'Message'),onChanged:(_)=>changed()),
        DropdownButtonFormField<String>(key:ValueKey(priority),initialValue:priority,decoration:const InputDecoration(labelText:'Priority'),items:const[DropdownMenuItem(value:'general',child:Text('General')),DropdownMenuItem(value:'important',child:Text('Important')),DropdownMenuItem(value:'urgent',child:Text('Urgent')),DropdownMenuItem(value:'maintenance',child:Text('Maintenance'))],onChanged:busy?null:(v)=>setState((){priority=v??'general';requestKey=null;})),
        SwitchListTile(title:const Text('Pin in inbox'),subtitle:const Text('Protect from user deletion'),value:pinned,onChanged:busy?null:(v)=>setState((){pinned=v;requestKey=null;})),
        SwitchListTile(title:const Text('Require acknowledgment'),value:requireAck,onChanged:busy?null:(v)=>setState((){requireAck=v;requestKey=null;})),
        SwitchListTile(title:const Text('Inbox only'),subtitle:const Text('Do not send phone alerts'),value:inboxOnly,onChanged:busy?null:(v)=>setState((){inboxOnly=v;requestKey=null;})),
        Text(inboxOnly?'Inbox preview':'Notification preview',style:theme.textTheme.titleMedium),
        Card(child:ListTile(leading:const Icon(Icons.notifications_active_outlined),title:Text(title.text.isEmpty?'Your title':title.text),subtitle:Text(body.text.isEmpty?'Your message':body.text))),
        const SizedBox(height:12),
        SegmentedButton<String>(segments:const[ButtonSegment(value:'now',label:Text('Send now')),ButtonSegment(value:'scheduled',label:Text('Schedule'))],selected:{mode},onSelectionChanged:busy?null:(v)=>setState((){mode=v.first;requestKey=null;})),
        if(mode=='scheduled')...[
          ListTile(leading:const Icon(Icons.calendar_month),title:Text(when==null?'Pick local date and time':dateText(when!.millisecondsSinceEpoch~/1000)),onTap:busy?null:pickDate),
          DropdownButtonFormField<int>(key:ValueKey(repeat),initialValue:repeat,decoration:const InputDecoration(labelText:'Repeat (fixed 24-hour days)'),items:const[DropdownMenuItem(value:0,child:Text('Once')),DropdownMenuItem(value:1,child:Text('Every day')),DropdownMenuItem(value:7,child:Text('Every 7 days')),DropdownMenuItem(value:30,child:Text('Every 30 days'))],onChanged:busy?null:(v)=>setState((){repeat=v??0;requestKey=null;})),
        ],
        const SizedBox(height:12),
        FilledButton.icon(onPressed:busy||!valid?null:()=>save(mode),icon:Icon(mode=='now'?Icons.send:Icons.schedule),label:Text(busy?'Working…':mode=='scheduled'?'Schedule announcement':audience=='self'?'Send test':'Send announcement')),
        Wrap(spacing:8,children:[OutlinedButton(onPressed:busy||!valid?null:()=>save('draft'),child:const Text('Save draft')),OutlinedButton(onPressed:busy||!valid?null:()=>save('template'),child:const Text('Save template'))]),
        if(status!=null)Padding(padding:const EdgeInsets.all(8),child:Text(status!)),
        const Divider(height:36),Text('Message library & history',style:theme.textTheme.titleLarge),
        TextField(controller:search,decoration:const InputDecoration(prefixIcon:Icon(Icons.search),hintText:'Search messages'),onChanged:(_)=>setState((){})),
        Wrap(spacing:8,children:[for(final e in {'all':'All','scheduled':'Scheduled','saved':'Drafts & templates','history':'History'}.entries)ChoiceChip(label:Text(e.value),selected:filter==e.key,onSelected:(_)=>setState(()=>filter=e.key))]),
        Wrap(spacing:8,children:[TextButton(onPressed:busy?null:()=>remove(null),child:const Text('Clear history')),TextButton(onPressed:busy?null:()=>remove(null,old:true),child:const Text('Clear older than 30 days')),TextButton(onPressed:()=>Clipboard.setData(ClipboardData(text:const JsonEncoder.withIndent('  ').convert({'v2':jobs,'v1':legacy}))),child:const Text('Copy history JSON'))]),
        if(loading)const LinearProgressIndicator()else if(jobs.where(matches).isEmpty)const Text('No matching V2 messages.'),
        for(final j in jobs.where(matches))job(j),
        if((filter=='all'||filter=='history')&&legacy.where(matches).isNotEmpty)...[
          const Divider(),Text('Previous V1 history',style:theme.textTheme.titleMedium),
          for(final j in legacy.where(matches))Card(child:ListTile(title:Text('${j['title']}'),subtitle:Text('${j['body']}\n${j['created']} UTC · ${j['status']} · Accepted ${j['sent']} · Failed ${j['failed']}'),trailing:IconButton(onPressed:busy?null:()=>reuse(j),icon:const Icon(Icons.copy),tooltip:'Copy to composer'))),
        ],
      ]))),
    );
  }
}
