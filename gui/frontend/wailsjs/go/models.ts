export namespace protocol {
	
	export class AttackReply {
	    attacker_hp: number;
	    target_hp: number;
	    damage: number;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new AttackReply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.attacker_hp = source["attacker_hp"];
	        this.target_hp = source["target_hp"];
	        this.damage = source["damage"];
	        this.status = source["status"];
	    }
	}
	export class Room {
	    id: string;
	    name: string;
	    description: string;
	    exits: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new Room(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.exits = source["exits"];
	    }
	}
	export class LookReply {
	    room: Room;
	    players: string[];
	    items: string[];
	    npcs: string[];
	
	    static createFrom(source: any = {}) {
	        return new LookReply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.room = this.convertValues(source["room"], Room);
	        this.players = source["players"];
	        this.items = source["items"];
	        this.npcs = source["npcs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class QuestEntry {
	    quest_id: string;
	    status: string;
	    progress?: string;
	
	    static createFrom(source: any = {}) {
	        return new QuestEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.quest_id = source["quest_id"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	    }
	}
	export class QuestReply {
	    quest_id: string;
	    description: string;
	    reward: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new QuestReply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.quest_id = source["quest_id"];
	        this.description = source["description"];
	        this.reward = source["reward"];
	        this.status = source["status"];
	    }
	}
	
	export class StatusReply {
	    hp: number;
	    max_hp: number;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusReply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hp = source["hp"];
	        this.max_hp = source["max_hp"];
	        this.status = source["status"];
	    }
	}

}

